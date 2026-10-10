//go:build linux

package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

const checkpointLimit = 64 << 10

// FileStore owns one explicit, private operation directory. A nonblocking kernel
// lock prevents two runners from acknowledging the same checkpoint. Neither
// Close nor a failed Save removes a retained checkpoint or backup. Interrupted
// pending files are retained for assessment, never silently reused or deleted.
// This store authenticates local owner/plan identity, not release signatures.
type FileStore struct {
	mu                                                  sync.Mutex
	directory                                           string
	root                                                *os.Root
	lock                                                *os.File
	directoryIdentity, lockIdentity, checkpointIdentity os.FileInfo
	plan                                                Plan
	closed                                              bool
	lastState                                           *Checkpoint
	initialized                                         bool
}

func OpenFileStore(directory string, plan Plan) (*FileStore, error) {
	if plan.Validate() != nil || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, errors.New("explicit valid plan/private directory required")
	}
	before, err := os.Lstat(directory)
	if err != nil || !ownedMode(before, 0o700, true) {
		return nil, errors.New("owned private0700 checkpoint directory required")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return nil, errors.New("checkpoint directory alias rejected")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("open private checkpoint scope")
	}
	store := &FileStore{directory: directory, root: root, directoryIdentity: before, plan: plan}
	failed := true
	defer func() {
		if failed {
			_ = store.Close()
		}
	}()
	if err = store.directoryFence(); err != nil {
		return nil, err
	}
	lock, err := root.OpenFile("checkpoint.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		lock, err = root.OpenFile("checkpoint.lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return nil, errors.New("private checkpoint lock unavailable")
	}
	store.lock = lock
	store.lockIdentity, err = lock.Stat()
	named, namedErr := root.Lstat("checkpoint.lock")
	if err != nil || namedErr != nil || !ownedMode(named, 0o600, false) || !os.SameFile(named, store.lockIdentity) {
		return nil, errors.New("private checkpoint lock identity differs")
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return nil, errors.New("checkpoint already owned by another runner")
	}
	bounded, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if _, err = store.Load(bounded); err != nil {
		return nil, err
	}
	store.initialized = true
	failed = false
	return store, nil
}

func ownedMode(info os.FileInfo, mode os.FileMode, directory bool) bool {
	if info == nil || info.Mode().Perm() != mode {
		return false
	}
	if directory {
		if !info.IsDir() {
			return false
		}
	} else if !info.Mode().IsRegular() {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && owner.Uid == uint32(os.Getuid())
}
func (s *FileStore) directoryFence() error {
	actual, err := s.root.Stat(".")
	named, namedErr := os.Lstat(s.directory)
	if err != nil || namedErr != nil || !ownedMode(named, 0o700, true) || !os.SameFile(actual, s.directoryIdentity) || !os.SameFile(named, s.directoryIdentity) {
		return errors.New("private checkpoint directory identity changed")
	}
	return nil
}
func (s *FileStore) fence() error {
	if s.closed || s.root == nil || s.lock == nil {
		return errors.New("checkpoint store closed")
	}
	if s.directoryFence() != nil {
		return errors.New("private checkpoint directory identity changed")
	}
	named, err := s.root.Lstat("checkpoint.lock")
	if err != nil || !ownedMode(named, 0o600, false) || !os.SameFile(named, s.lockIdentity) {
		return errors.New("private checkpoint lock identity changed")
	}
	return s.existingCheckpoint()
}
func (s *FileStore) existingCheckpoint() error {
	named, err := s.root.Lstat("checkpoint.json")
	if errors.Is(err, os.ErrNotExist) {
		if s.checkpointIdentity != nil {
			return errors.New("retained checkpoint disappeared")
		}
		return nil
	}
	if err != nil || !ownedMode(named, 0o600, false) {
		return errors.New("private regular0600 checkpoint required")
	}
	if s.initialized && s.checkpointIdentity == nil {
		return errors.New("unobserved checkpoint appeared after ownership admission")
	}
	if s.checkpointIdentity != nil && !os.SameFile(named, s.checkpointIdentity) {
		return errors.New("retained checkpoint identity changed")
	}
	s.checkpointIdentity = named
	return nil
}

func (s *FileStore) Load(ctx context.Context) (*Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		return nil, errors.New("explicit checkpoint context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.fence(); err != nil {
		return nil, err
	}
	if s.checkpointIdentity == nil {
		return nil, nil
	}
	file, err := s.root.OpenFile("checkpoint.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("read private checkpoint")
	}
	defer func() { _ = file.Close() }()
	actual, err := file.Stat()
	if err != nil || !ownedMode(actual, 0o600, false) || !os.SameFile(actual, s.checkpointIdentity) {
		return nil, errors.New("opened checkpoint identity changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, checkpointLimit+1))
	if err != nil || len(raw) > checkpointLimit {
		return nil, errors.New("private checkpoint exceeds read bound")
	}
	state, err := decodeCheckpoint(raw, s.plan)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = s.fence(); err != nil {
		return nil, err
	}
	copyState := state
	copyState.Completed = append([]Stage{}, state.Completed...)
	s.lastState = &copyState
	return &state, nil
}
func decodeCheckpoint(raw []byte, plan Plan) (Checkpoint, error) {
	var state Checkpoint
	if err := jsoncontract.Decode(raw, &state); err != nil {
		return state, errors.New("private checkpoint schema invalid")
	}
	var object map[string]json.RawMessage
	err := jsoncontract.Decode(raw, &object)
	if err != nil {
		return state, errors.New("private checkpoint object invalid")
	}
	for _, value := range object {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return state, errors.New("null checkpoint field rejected")
		}
	}
	for _, name := range []string{"schema_version", "target", "plan_digest", "identity", "completed", "applied", "replaced", "rollback_attempted"} {
		value, exists := object[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return state, errors.New("private checkpoint required field absent")
		}
	}
	if state.Completed == nil || validateCheckpoint(state, plan) != nil {
		return state, errors.New("private checkpoint plan/stage binding invalid")
	}
	return state, nil
}

func (s *FileStore) Save(ctx context.Context, state Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		return errors.New("explicit checkpoint context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if validateCheckpoint(state, s.plan) != nil || checkpointRegresses(s.lastState, state) {
		return errors.New("checkpoint contradicts bound plan or acknowledged progress")
	}
	if err := s.fence(); err != nil {
		return err
	}
	// Canonical arrays remain arrays even before the first completed stage.
	state.Completed = append([]Stage{}, state.Completed...)
	payload, err := json.Marshal(state)
	if err != nil || len(payload) > checkpointLimit {
		return errors.New("bounded checkpoint encoding failed")
	}
	pending, err := s.root.OpenFile("checkpoint.pending", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("interrupted or existing checkpoint intent requires assessment")
	}
	defer func() { _ = pending.Close() }()
	identity, err := pending.Stat()
	if err != nil {
		return errors.New("private checkpoint intent identity unavailable")
	}
	// On every failure leave the owned pending bytes for diagnosis/assessment.
	// A future operation cannot overwrite the last acknowledged checkpoint.
	if _, err = pending.Write(payload); err != nil {
		return errors.New("write private checkpoint intent")
	}
	if err = pending.Sync(); err != nil {
		return errors.New("sync private checkpoint intent")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.fence(); err != nil {
		return err
	}
	named, err := s.root.Lstat("checkpoint.pending")
	if err != nil || !ownedMode(named, 0o600, false) || !os.SameFile(named, identity) {
		return errors.New("private checkpoint intent identity changed")
	}
	if err = s.root.Rename("checkpoint.pending", "checkpoint.json"); err != nil {
		return errors.New("publish private checkpoint intent")
	}
	s.checkpointIdentity = identity
	copyState := state
	copyState.Completed = append([]Stage{}, state.Completed...)
	s.lastState = &copyState
	directory, err := s.root.Open(".")
	if err != nil {
		return errors.New("open checkpoint sync scope")
	}
	defer func() { _ = directory.Close() }()
	if err = directory.Sync(); err != nil {
		return errors.New("sync checkpoint publication")
	}
	return ctx.Err() // Acknowledgement must not outlive caller cancellation.
}
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var lockErr, rootErr error
	if s.lock != nil {
		lockErr = s.lock.Close()
	}
	if s.root != nil {
		rootErr = s.root.Close()
	}
	return errors.Join(lockErr, rootErr)
}

func checkpointRegresses(previous *Checkpoint, next Checkpoint) bool {
	if previous == nil {
		return len(next.Completed) != 0 || next.Pending != Verify || next.Applied || next.Replaced || next.RollbackAttempted
	}
	if len(next.Completed) < len(previous.Completed) || len(next.Completed) > len(previous.Completed)+1 || (previous.Applied && !next.Applied) || (previous.Replaced && !next.Replaced) || (previous.RollbackAttempted && !next.RollbackAttempted) {
		return true
	}
	for i, stage := range previous.Completed {
		if next.Completed[i] != stage {
			return true
		}
	}
	if len(next.Completed) > len(previous.Completed) {
		return previous.Pending == "" || next.Completed[len(previous.Completed)] != previous.Pending || next.Pending != ""
	}
	if previous.Pending != "" && next.Pending != previous.Pending {
		return true
	}
	return false
}
