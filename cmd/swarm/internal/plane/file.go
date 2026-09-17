package plane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// File is the single-host store: the same transitions, made atomic by an
// OS advisory lock and durable by a synced write and rename. Many
// processes on one machine share it. It is not for peers that do not share
// a filesystem; that is what the RESP backend is for.
type File struct {
	dir    string
	Broken Broken
	// CrashAfterWrite, when set, is called after the new state is durable
	// and before the lock is released; the fault harness uses it to die at
	// the worst moment.
	CrashAfterWrite func(op string)
}

// OpenFile opens or creates a file store in dir.
func OpenFile(dir string) (*File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &File{dir: dir}, nil
}

func (f *File) path(name string) string { return filepath.Join(f.dir, name) }

// step runs one transition under the lock: load, apply, write, sync.
func (f *File) step(op string, mutate bool, fn func(d *Data, now time.Time) error) error {
	lf, err := os.OpenFile(f.path("plane.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	deadline := time.Now().Add(30 * time.Second)
	for {
		ok, err := tryLock(lf)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("plane: store lock not acquired in 30s")
		}
		time.Sleep(2 * time.Millisecond)
	}
	defer unlock(lf)
	d := NewData()
	data, err := os.ReadFile(f.path("plane.json"))
	switch {
	case err == nil:
		if err := json.Unmarshal(data, d); err != nil {
			return fmt.Errorf("plane: state unreadable: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	ferr := fn(d, time.Now().UTC())
	if !mutate {
		return ferr
	}
	// A refusal is logged too, so the state is written either way.
	out, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := writeSynced(f.path("plane.json"), out); err != nil {
		return err
	}
	if f.CrashAfterWrite != nil {
		f.CrashAfterWrite(op)
	}
	return ferr
}

func writeSynced(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	w, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Sync(); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (f *File) Put(_ context.Context, it Item) error {
	return f.step("put", true, func(d *Data, now time.Time) error { d.put(now, it); return nil })
}

func (f *File) Claim(_ context.Context, kind, id, inc string, ttl time.Duration, idem string) (g Grant, err error) {
	err = f.step("claim", true, func(d *Data, now time.Time) (e error) {
		g, e = d.claim(now, kind, id, inc, ttl, idem, f.Broken)
		return
	})
	return
}

func (f *File) ClaimNext(_ context.Context, kind, inc string, ttl time.Duration, idem string) (g Grant, err error) {
	err = f.step("claim", true, func(d *Data, now time.Time) (e error) {
		g, e = d.claimNext(now, kind, inc, ttl, idem, f.Broken)
		return
	})
	return
}

func (f *File) Renew(_ context.Context, g Grant, ttl time.Duration) (out Grant, err error) {
	err = f.step("renew", true, func(d *Data, now time.Time) (e error) { out, e = d.renew(now, g, ttl); return })
	return
}

func (f *File) Release(_ context.Context, g Grant) error {
	return f.step("release", true, func(d *Data, now time.Time) error { return d.release(now, g, f.Broken) })
}

func (f *File) Commit(_ context.Context, g Grant, result, idem string, emit []Item) (r Receipt, err error) {
	err = f.step("commit", true, func(d *Data, now time.Time) (e error) { r, e = d.commit(now, g, result, idem, emit, f.Broken); return })
	return
}

func (f *File) Get(_ context.Context, kind, id string) (it Item, err error) {
	err = f.step("get", false, func(d *Data, _ time.Time) error {
		p, ok := d.Items[key(kind, id)]
		if !ok {
			return ErrNotFound
		}
		it = *p
		return nil
	})
	return
}

func (f *File) List(_ context.Context, kind string) (out []Item, err error) {
	err = f.step("list", false, func(d *Data, _ time.Time) error { out = d.list(kind); return nil })
	return
}

func (f *File) History(context.Context) (out []Event, err error) {
	err = f.step("history", false, func(d *Data, _ time.Time) error { out = append([]Event(nil), d.Log...); return nil })
	return
}

func (f *File) Close() error { return nil }
