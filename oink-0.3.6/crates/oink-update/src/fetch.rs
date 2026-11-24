//! Catalog lookup across the configured sources and HTTPS transfers, shared by
//! the Windows service and the macOS backend.
use crate::Error;

/// Resolves a catalog path against an HTTPS source origin. GitHub Releases
/// download endpoints flatten `/` separators in asset names to `-`.
pub fn catalog_url(source: &str, path: &str) -> String {
    if source.ends_with("/releases/latest/download") || source.contains("/releases/download/") {
        format!("{source}/{}", path.replace('/', "-"))
    } else {
        format!("{source}/{path}")
    }
}

/// Tries each source in order and returns the first catalog that verifies.
/// Any failure moves on to the next source. When all fail, a verification
/// failure is reported in preference to a network failure: it says more.
pub fn first_verified<T>(
    sources: &[String],
    path: &str,
    mut get: impl FnMut(&str) -> Result<Vec<u8>, Error>,
    mut verify: impl FnMut(&[u8]) -> Result<T, Error>,
) -> Result<T, Error> {
    let mut failure = None;
    for source in sources {
        match get(&catalog_url(source, path)).and_then(|bytes| verify(&bytes)) {
            Ok(value) => return Ok(value),
            Err(error) => {
                if failure.is_none_or(|first| first == Error::Network) {
                    failure = Some(error);
                }
            }
        }
    }
    Err(failure.unwrap_or(Error::NotConfigured))
}

/// GET with validated HTTPS redirects (required for GitHub Releases CDN hops).
/// `progress` receives (received, expected) as bytes arrive.
#[cfg(feature = "http")]
pub fn get(url: &str, limit: u64, package: bool, progress: &mut dyn FnMut(u64, u64)) -> Result<Vec<u8>, Error> {
    get_until(url, limit, package, &mut |received, expected| {
        progress(received, expected);
        true
    })
}
/// As [`get`], but the transfer is abandoned (as `Error::Network`) as soon as
/// `progress` returns false.
#[cfg(feature = "http")]
pub fn get_until(
    url: &str,
    limit: u64,
    package: bool,
    progress: &mut dyn FnMut(u64, u64) -> bool,
) -> Result<Vec<u8>, Error> {
    use std::{io::Read, time::Duration};
    let _ = package;
    let policy = reqwest::redirect::Policy::custom(|attempt| {
        if crate::feed::validate_redirect(attempt.url().as_str(), attempt.previous().len()).is_ok() {
            attempt.follow()
        } else {
            attempt.stop()
        }
    });
    // The client timeout bounds connecting and each read, not the whole body,
    // so a slow but live download is never cut off.
    let client = reqwest::blocking::Client::builder()
        .https_only(!cfg!(feature = "test-feed"))
        .redirect(policy)
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(60))
        .user_agent("Oink-updater/2")
        .build()
        .map_err(|_| Error::Network)?;
    let response = client.get(url).send().map_err(|_| Error::Network)?;
    if response.status() != reqwest::StatusCode::OK || response.content_length().is_some_and(|n| n > limit) {
        return Err(Error::Network);
    }
    let expected = response.content_length().unwrap_or(limit);
    let mut body = response.take(limit + 1);
    let mut bytes = Vec::with_capacity(expected.min(limit) as usize);
    let mut chunk = vec![0; 256 * 1024];
    loop {
        let read = body.read(&mut chunk).map_err(|_| Error::Network)?;
        if read == 0 {
            break;
        }
        bytes.extend_from_slice(&chunk[..read]);
        if bytes.len() as u64 > limit {
            return Err(Error::Limit);
        }
        if !progress(bytes.len() as u64, expected) {
            return Err(Error::Network);
        }
    }
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn sources() -> Vec<String> {
        vec![
            "https://a.example".into(),
            "https://b.example".into(),
            "https://c.example".into(),
        ]
    }
    #[test]
    fn the_first_source_that_verifies_wins() {
        let mut asked = vec![];
        let result = first_verified(
            &sources(),
            "windows/v2/x/manifest.json",
            |url| {
                asked.push(url.to_owned());
                match url {
                    "https://a.example/windows/v2/x/manifest.json" => Err(Error::Network),
                    "https://b.example/windows/v2/x/manifest.json" => Ok(b"stale".to_vec()),
                    _ => Ok(b"good".to_vec()),
                }
            },
            |bytes| if bytes == b"good" { Ok(7) } else { Err(Error::Replay) },
        );
        assert_eq!(result, Ok(7));
        assert_eq!(asked.len(), 3);
    }
    #[test]
    fn a_verification_failure_outranks_network_failures() {
        let fail = |error| {
            first_verified(
                &sources(),
                "p",
                |url| {
                    if url.starts_with("https://b") {
                        Ok(vec![])
                    } else {
                        Err(Error::Network)
                    }
                },
                |_| Err::<(), _>(error),
            )
        };
        assert_eq!(fail(Error::Signature), Err(Error::Signature));
        assert_eq!(
            first_verified(&sources(), "p", |_| Err(Error::Network), |_| Ok(())),
            Err(Error::Network)
        );
        assert_eq!(
            first_verified(&[], "p", |_| Ok(vec![]), |_| Ok(())),
            Err(Error::NotConfigured)
        );
    }
    #[test]
    fn github_releases_origin_flattens_catalog_asset_name() {
        assert_eq!(
            catalog_url(
                "https://github.com/shuozhang7979/LogitechFlow/releases/latest/download",
                "mac/v2/mac-2026-01/arm64/stable/manifest.json"
            ),
            "https://github.com/shuozhang7979/LogitechFlow/releases/latest/download/mac-v2-mac-2026-01-arm64-stable-manifest.json"
        );
        assert_eq!(
            catalog_url(
                "https://raw.githubusercontent.com/shuozhang7979/LogitechFlow/built-installers/feed",
                "mac/v2/mac-2026-01/arm64/stable/manifest.json"
            ),
            "https://raw.githubusercontent.com/shuozhang7979/LogitechFlow/built-installers/feed/mac/v2/mac-2026-01/arm64/stable/manifest.json"
        );
    }
}
