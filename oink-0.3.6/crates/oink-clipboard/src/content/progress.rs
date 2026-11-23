//! Observable state for native file/representation delivery and real-time
//! bidirectional transfer monitoring across HUD, desktop UI and CLI.
use super::Unavailable;
use crate::model::{ContentSize, Demand, FileNode, NodeId, NodeKind};
use oink_foundation::{DeviceId, content::OfferId};
use serde::{Deserialize, Serialize};
use std::{
    collections::{HashMap, HashSet, VecDeque},
    sync::{
        Arc, Mutex, OnceLock,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub enum Phase {
    Offered,
    Preparing,
    Receiving,
    Reconnecting,
    Stored,
    Failed,
    Cancelled,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Direction {
    Sending,
    Receiving,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Kind {
    Clipboard,
    DragAndDrop,
}

impl Kind {
    pub fn from_demand(demand: Demand, is_drag_link: bool) -> Self {
        if is_drag_link || matches!(demand, Demand::Hover(_) | Demand::Drop(_)) {
            Self::DragAndDrop
        } else {
            Self::Clipboard
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Progress {
    pub phase: Phase,
    pub discovered: u64,
    pub files_done: u64,
    pub bytes: u64,
    pub total_files: Option<u64>,
    pub total_bytes: Option<u64>,
    pub failure: Option<Unavailable>,
    pub source_acknowledged: bool,
    /// Content-derived receipt is carried separately, never in this observable
    /// diagnostic record. Stored means durable local readers are available.
    pub enumerating: bool,
}

impl Progress {
    pub(super) fn new() -> Self {
        Self {
            phase: Phase::Offered,
            discovered: 0,
            files_done: 0,
            bytes: 0,
            total_files: None,
            total_bytes: None,
            failure: None,
            source_acknowledged: false,
            enumerating: false,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct ActiveTransfer {
    pub id: String,
    pub owner: Option<DeviceId>,
    pub offer: OfferId,
    pub peer: DeviceId,
    pub peer_name: Option<String>,
    pub direction: Direction,
    pub kind: Kind,
    pub current_file: Option<String>,
    pub done: u64,
    pub total: u64,
    pub files_done: usize,
    pub files: usize,
    pub speed_bps: u64,
    pub eta_secs: Option<u64>,
    pub phase: Phase,
    pub enumerating: bool,
    pub failure: Option<Unavailable>,
}

const EMIT_THROTTLE: Duration = Duration::from_millis(80);
const SPEED_WINDOW: Duration = Duration::from_secs(2);
const TERMINAL_RETENTION: Duration = Duration::from_secs(4);
const IDLE_EXPIRY: Duration = Duration::from_secs(45);
const MAX_ENTRIES: usize = 64;

type CancelHook = Arc<dyn Fn() + Send + Sync>;

struct Entry {
    id: String,
    owner: Option<DeviceId>,
    offer: OfferId,
    peer: DeviceId,
    peer_name: Option<String>,
    direction: Direction,
    kind: Kind,
    current_file: Option<String>,
    done: u64,
    explicit_total: Option<u64>,
    explicit_files: Option<usize>,
    explicit_files_done: Option<usize>,
    known_nodes: HashMap<u64, (u64, bool, String)>,
    pending_dirs: HashSet<u64>,
    completed_files: HashSet<u64>,
    high_water: HashMap<(u8, u64), u64>,
    samples: VecDeque<(Instant, u64)>,
    smoothed_bps: f64,
    phase: Phase,
    failure: Option<Unavailable>,
    remote_managed: bool,
    started_at: Instant,
    updated_at: Instant,
    completed_at: Option<Instant>,
    cancelled: Arc<AtomicBool>,
    cancels: Vec<CancelHook>,
}

impl Entry {
    fn new(
        id: String,
        owner: Option<DeviceId>,
        offer: OfferId,
        peer: DeviceId,
        direction: Direction,
        kind: Kind,
        now: Instant,
    ) -> Self {
        let mut samples = VecDeque::with_capacity(32);
        samples.push_back((now, 0));
        Self {
            id,
            owner,
            offer,
            peer,
            peer_name: None,
            direction,
            kind,
            current_file: None,
            done: 0,
            explicit_total: None,
            explicit_files: None,
            explicit_files_done: None,
            known_nodes: HashMap::new(),
            pending_dirs: HashSet::new(),
            completed_files: HashSet::new(),
            high_water: HashMap::new(),
            samples,
            smoothed_bps: 0.0,
            phase: Phase::Preparing,
            failure: None,
            remote_managed: false,
            started_at: now,
            updated_at: now,
            completed_at: None,
            cancelled: Arc::new(AtomicBool::new(false)),
            cancels: Vec::new(),
        }
    }

    fn is_terminal(&self) -> bool {
        matches!(self.phase, Phase::Stored | Phase::Failed | Phase::Cancelled)
    }

    fn observe_node(&mut self, node: &FileNode) {
        let is_file = node.kind != NodeKind::Directory;
        let known_size = match node.size {
            ContentSize::Known(n) if is_file => n,
            _ => 0,
        };
        if node.kind == NodeKind::Directory && !self.known_nodes.contains_key(&node.id.0) {
            self.pending_dirs.insert(node.id.0);
        }
        self.known_nodes
            .entry(node.id.0)
            .and_modify(|(size, file_flag, name)| {
                if known_size > *size {
                    *size = known_size;
                }
                *file_flag = is_file;
                if !node.name.is_empty() {
                    *name = node.name.clone();
                }
            })
            .or_insert_with(|| (known_size, is_file, node.name.clone()));
    }

    fn computed_files(&self) -> usize {
        let discovered = self.known_nodes.values().filter(|(_, is_file, _)| *is_file).count();
        discovered
            .max(self.explicit_files.unwrap_or(0))
            .max(self.files_done())
    }

    fn files_done(&self) -> usize {
        self.completed_files
            .len()
            .max(self.explicit_files_done.unwrap_or(0))
    }

    fn computed_total(&self) -> u64 {
        let discovered: u64 = self.known_nodes.values().map(|(sz, _, _)| *sz).sum();
        discovered
            .max(self.explicit_total.unwrap_or(0))
            .max(self.done)
    }

    fn record_progress(&mut self, domain: u8, item: u64, through: u64, now: Instant) -> u64 {
        let prev = self.high_water.entry((domain, item)).or_default();
        if through <= *prev {
            return 0;
        }
        let delta = through - *prev;
        *prev = through;
        self.done = self.done.saturating_add(delta);
        self.updated_at = now;
        if matches!(self.phase, Phase::Offered | Phase::Preparing | Phase::Reconnecting) {
            self.phase = Phase::Receiving;
        }
        self.update_speed(now);
        delta
    }

    fn update_speed(&mut self, now: Instant) {
        self.samples.push_back((now, self.done));
        while self.samples.len() > 2
            && let Some((front_at, _)) = self.samples.front()
            && now.saturating_duration_since(*front_at) > SPEED_WINDOW
        {
            self.samples.pop_front();
        }
        if let Some((first_at, first_bytes)) = self.samples.front().copied() {
            let dt = now.saturating_duration_since(first_at).as_secs_f64();
            if dt >= 0.04 && self.done > first_bytes {
                let instant = (self.done - first_bytes) as f64 / dt;
                self.smoothed_bps = if self.smoothed_bps <= 1.0 {
                    instant
                } else {
                    0.35 * instant + 0.65 * self.smoothed_bps
                };
            }
        }
    }

    fn finalize_if_all_files_done(&mut self, now: Instant) -> bool {
        if self.is_terminal() || self.remote_managed {
            return false;
        }
        let files = self.computed_files();
        let total = self.computed_total();
        if self.pending_dirs.is_empty()
            && files > 0
            && self.completed_files.len() >= files
            && self.done >= total
        {
            self.mark_stored(now);
            return true;
        }
        false
    }

    fn mark_stored(&mut self, now: Instant) {
        self.phase = Phase::Stored;
        self.pending_dirs.clear();
        self.updated_at = now;
        self.completed_at = Some(now);
        let total = self.computed_total();
        if total > self.done {
            self.done = total;
        }
        let dt = now.saturating_duration_since(self.started_at).as_secs_f64();
        if self.done > 0 && dt >= 0.02 {
            let avg = self.done as f64 / dt;
            if self.smoothed_bps <= 1.0 || avg > 0.0 {
                self.smoothed_bps = avg;
            }
        }
    }

    fn snapshot(&self, now: Instant) -> ActiveTransfer {
        let total = self.computed_total();
        let files = self.computed_files();
        let files_done = self.files_done().min(files.max(self.completed_files.len()));
        let speed_bps = if matches!(self.phase, Phase::Receiving) {
            if now.saturating_duration_since(self.updated_at) > Duration::from_secs(3) {
                0
            } else {
                self.smoothed_bps.round() as u64
            }
        } else if matches!(self.phase, Phase::Stored) {
            self.smoothed_bps.round() as u64
        } else {
            0
        };
        let eta_secs = if matches!(self.phase, Phase::Receiving) && speed_bps > 0 && total > self.done {
            Some((total - self.done).div_ceil(speed_bps))
        } else {
            None
        };
        ActiveTransfer {
            id: self.id.clone(),
            owner: self.owner,
            offer: self.offer,
            peer: self.peer,
            peer_name: self.peer_name.clone(),
            direction: self.direction,
            kind: self.kind,
            current_file: self.current_file.clone(),
            done: self.done,
            total,
            files_done,
            files,
            speed_bps,
            eta_secs,
            phase: self.phase,
            enumerating: !self.pending_dirs.is_empty() && !self.is_terminal(),
            failure: self.failure,
        }
    }
}

pub struct TransferTracker {
    entries: Mutex<HashMap<String, Entry>>,
    last_emit: Mutex<Instant>,
    notify: tokio::sync::watch::Sender<u64>,
}

impl std::fmt::Debug for TransferTracker {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("TransferTracker").finish_non_exhaustive()
    }
}

impl Default for TransferTracker {
    fn default() -> Self {
        Self::new()
    }
}

pub fn rx_transfer_id(offer: OfferId) -> String {
    format!(
        "rx:{}:{}:{}",
        offer.clip.origin.short(),
        offer.clip.seq,
        offer.generation
    )
}

pub fn tx_transfer_id(offer: OfferId, peer: DeviceId) -> String {
    format!(
        "tx:{}:{}:{}:{}",
        peer.short(),
        offer.clip.origin.short(),
        offer.clip.seq,
        offer.generation
    )
}

impl TransferTracker {
    pub fn new() -> Self {
        Self {
            entries: Mutex::new(HashMap::new()),
            last_emit: Mutex::new(Instant::now() - EMIT_THROTTLE),
            notify: tokio::sync::watch::channel(0).0,
        }
    }

    pub fn subscribe(&self) -> tokio::sync::watch::Receiver<u64> {
        self.notify.subscribe()
    }

    fn emit(&self, force: bool, now: Instant) {
        let mut last = self.last_emit.lock().unwrap();
        if force || now.saturating_duration_since(*last) >= EMIT_THROTTLE {
            *last = now;
            drop(last);
            self.notify.send_modify(|seq| *seq = seq.wrapping_add(1));
        }
    }

    fn prune_locked(entries: &mut HashMap<String, Entry>, now: Instant) {
        entries.retain(|_, entry| match entry.completed_at {
            Some(at) => now.saturating_duration_since(at) <= TERMINAL_RETENTION,
            None => entry.remote_managed || now.saturating_duration_since(entry.updated_at) <= IDLE_EXPIRY,
        });
        while entries.len() > MAX_ENTRIES {
            let Some(oldest) = entries
                .iter()
                .min_by_key(|(_, e)| e.updated_at)
                .map(|(k, _)| k.clone())
            else {
                break;
            };
            entries.remove(&oldest);
        }
    }

    #[allow(clippy::too_many_arguments)]
    pub fn begin(
        &self,
        id: String,
        owner: Option<DeviceId>,
        offer: OfferId,
        peer: DeviceId,
        peer_name: Option<String>,
        direction: Direction,
        kind: Kind,
        roots: &[FileNode],
        remote_managed: bool,
        cancel: Option<CancelHook>,
    ) -> Arc<AtomicBool> {
        let now = Instant::now();
        let flag = {
            let mut entries = self.entries.lock().unwrap();
            Self::prune_locked(&mut entries, now);
            let entry = entries
                .entry(id.clone())
                .or_insert_with(|| Entry::new(id, owner, offer, peer, direction, kind, now));
            if entry.is_terminal() && entry.phase != Phase::Cancelled {
                // Allow a subsequent on-demand file read on the same offer to reopen tracking.
                *entry = Entry::new(entry.id.clone(), owner, offer, peer, direction, kind, now);
            }
            if owner.is_some() {
                entry.owner = owner;
            }
            if let Some(name) = peer_name.filter(|s| !s.is_empty()) {
                entry.peer_name = Some(name);
            }
            if kind == Kind::DragAndDrop {
                entry.kind = Kind::DragAndDrop;
            }
            entry.remote_managed |= remote_managed;
            entry.updated_at = now;
            for root in roots {
                entry.observe_node(root);
            }
            if let Some(hook) = cancel {
                entry.cancels.push(hook);
            }
            if roots.len() == 1 && roots[0].kind != NodeKind::Directory && entry.current_file.is_none() {
                entry.current_file = Some(roots[0].name.clone());
            }
            entry.cancelled.clone()
        };
        self.emit(true, now);
        flag
    }

    pub fn observe_directory(&self, id: &str, directory: NodeId, children: &[FileNode], complete: bool) {
        let now = Instant::now();
        let mut force = false;
        {
            let mut entries = self.entries.lock().unwrap();
            let Some(entry) = entries.get_mut(id) else { return };
            if entry.is_terminal() {
                return;
            }
            entry.updated_at = now;
            for child in children {
                entry.observe_node(child);
            }
            if complete {
                entry.pending_dirs.remove(&directory.0);
                force = entry.finalize_if_all_files_done(now);
            }
        }
        self.emit(force, now);
    }

    pub fn observe_file_open(&self, id: &str, node: &FileNode) {
        let now = Instant::now();
        {
            let mut entries = self.entries.lock().unwrap();
            let Some(entry) = entries.get_mut(id) else { return };
            if entry.is_terminal() {
                return;
            }
            entry.observe_node(node);
            if !node.name.is_empty() {
                entry.current_file = Some(node.name.clone());
            }
            if matches!(entry.phase, Phase::Offered | Phase::Preparing) {
                entry.phase = Phase::Receiving;
            }
            entry.updated_at = now;
        }
        self.emit(false, now);
    }

    pub fn observe_bytes(&self, id: &str, domain: u8, item: u64, through: u64, file_name: Option<&str>) {
        let now = Instant::now();
        let changed = {
            let mut entries = self.entries.lock().unwrap();
            let Some(entry) = entries.get_mut(id) else { return };
            if entry.is_terminal() {
                return;
            }
            if let Some(name) = file_name.filter(|s| !s.is_empty()) {
                entry.current_file = Some(name.to_owned());
            } else if domain == 1
                && entry.current_file.is_none()
                && let Some((_, _, name)) = entry.known_nodes.get(&item)
                && !name.is_empty()
            {
                entry.current_file = Some(name.clone());
            }
            entry.record_progress(domain, item, through, now) > 0
        };
        if changed {
            self.emit(false, now);
        }
    }

    pub fn observe_file_complete(&self, id: &str, node: NodeId, size: u64) {
        let now = Instant::now();
        {
            let mut entries = self.entries.lock().unwrap();
            let Some(entry) = entries.get_mut(id) else { return };
            if entry.is_terminal() {
                return;
            }
            let _ = entry.record_progress(1, node.0, size, now);
            entry.completed_files.insert(node.0);
            entry.updated_at = now;
            let _ = entry.finalize_if_all_files_done(now);
        }
        self.emit(true, now);
    }

    pub fn sync_remote_progress(&self, id: &str, progress: &Progress) {
        let now = Instant::now();
        let mut force = false;
        {
            let mut entries = self.entries.lock().unwrap();
            let Some(entry) = entries.get_mut(id) else { return };
            if entry.phase == Phase::Cancelled {
                return;
            }
            if progress.bytes > entry.done {
                entry.done = progress.bytes;
                entry.update_speed(now);
            }
            if let Some(total) = progress.total_bytes {
                entry.explicit_total = Some(total);
            }
            if let Some(total_files) = progress.total_files {
                entry.explicit_files = Some(total_files as usize);
            }
            if progress.files_done > 0 {
                entry.explicit_files_done = Some(progress.files_done as usize);
            }
            entry.updated_at = now;
            match progress.phase {
                Phase::Stored => {
                    if entry.phase != Phase::Stored {
                        entry.mark_stored(now);
                        force = true;
                    }
                }
                Phase::Failed => {
                    if entry.phase != Phase::Failed {
                        entry.phase = Phase::Failed;
                        entry.failure = progress.failure;
                        entry.completed_at = Some(now);
                        force = true;
                    }
                }
                Phase::Cancelled => {
                    if entry.phase != Phase::Cancelled {
                        entry.phase = Phase::Cancelled;
                        entry.completed_at = Some(now);
                        force = true;
                    }
                }
                other => {
                    if !entry.is_terminal() && entry.done == 0 {
                        if entry.phase != other {
                            force = true;
                        }
                        entry.phase = other;
                    }
                }
            }
        }
        self.emit(force, now);
    }

    pub fn mark_receipt(&self, id: &str) {
        let now = Instant::now();
        let mut changed = false;
        {
            let mut entries = self.entries.lock().unwrap();
            if let Some(entry) = entries.get_mut(id)
                && !entry.is_terminal()
            {
                entry.mark_stored(now);
                changed = true;
            }
        }
        if changed {
            self.emit(true, now);
        }
    }

    pub fn mark_failed(&self, id: &str, reason: Unavailable) {
        let now = Instant::now();
        let mut changed = false;
        {
            let mut entries = self.entries.lock().unwrap();
            if let Some(entry) = entries.get_mut(id)
                && !entry.is_terminal()
            {
                entry.phase = if reason == Unavailable::Cancelled {
                    Phase::Cancelled
                } else {
                    Phase::Failed
                };
                entry.failure = Some(reason);
                entry.completed_at = Some(now);
                changed = true;
            }
        }
        if changed {
            self.emit(true, now);
        }
    }

    pub fn cancel(&self, id: &str) -> bool {
        let now = Instant::now();
        let hooks = {
            let mut entries = self.entries.lock().unwrap();
            let Some(entry) = entries.get_mut(id) else {
                return false;
            };
            entry.cancelled.store(true, Ordering::Release);
            entry.phase = Phase::Cancelled;
            entry.failure = Some(Unavailable::Cancelled);
            entry.updated_at = now;
            entry.completed_at = Some(now);
            entry.cancels.clone()
        };
        for hook in hooks {
            hook();
        }
        self.emit(true, now);
        true
    }

    pub fn snapshots_for(&self, owner: Option<DeviceId>) -> Vec<ActiveTransfer> {
        let now = Instant::now();
        let mut entries = self.entries.lock().unwrap();
        Self::prune_locked(&mut entries, now);
        let mut list: Vec<_> = entries
            .values()
            .filter(|entry| match owner {
                Some(me) => entry.owner == Some(me),
                None => true,
            })
            .map(|entry| (entry.started_at, entry.snapshot(now)))
            .collect();
        list.sort_by_key(|(started, _)| *started);
        list.into_iter().map(|(_, s)| s).collect()
    }
}

pub fn global_transfer_tracker() -> &'static TransferTracker {
    static TRACKER: OnceLock<TransferTracker> = OnceLock::new();
    TRACKER.get_or_init(TransferTracker::new)
}
