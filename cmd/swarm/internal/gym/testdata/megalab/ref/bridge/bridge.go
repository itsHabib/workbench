package bridge

import (
	"io"
	"strings"
	"sync"
	"time"

	"megalab/kv/store"
	"megalab/sched/clock"
	"megalab/sched/engine"
	"megalab/sched/plan"
	"megalab/sched/retry"
	"megalab/sched/state"
	"megalab/shop/app"
)

type Bridge struct {
	mu     sync.Mutex
	app    *app.App
	kv     *store.Store
	eng    *engine.Engine
	count  int
	orders map[string]string // run id -> order
	done   map[string]bool
}

func New(c clock.Clock, events io.Writer) (*Bridge, error) {
	b := &Bridge{app: app.New(c.Now, events), kv: store.New(c.Now), orders: map[string]string{}, done: map[string]bool{}}
	cfg := engine.Config{Retry: retry.Policy{Base: time.Second, Max: time.Minute, Factor: 2, Jitter: 0, MaxAttempts: 3},
		LeaseTTL: time.Minute, Workers: []string{"w1", "w2"}, Rand: func() float64 { return 0 }}
	eng, err := engine.New(c, cfg, func(t engine.Task) (string, error) { return t.Name, nil }, nil)
	if err != nil {
		return nil, err
	}
	if err := eng.Register(plan.Spec{Name: "fulfil", Tasks: map[string][]string{"pick": nil, "pack": {"pick"}, "ship": {"pack"}}}); err != nil {
		return nil, err
	}
	b.eng = eng
	return b, nil
}

func (b *Bridge) Exec(line string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	reply, err := b.app.Exec(line)
	if err != nil {
		return reply, err
	}
	b.count++
	n := itoa(b.count)
	b.kv.Set("cmd:"+n, line, 0)
	b.kv.Set("reply:"+n, reply, 0)
	fields := strings.Fields(line)
	if len(fields) >= 2 && strings.EqualFold(fields[0], "PAY") {
		id, err := b.eng.Submit("fulfil")
		if err != nil {
			return reply, err
		}
		b.orders[id] = fields[1]
		b.kv.Set("fulfil:"+fields[1], id, 0)
	}
	return reply, nil
}

func (b *Bridge) Tick() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.eng.Tick(); err != nil {
		return err
	}
	for _, r := range b.eng.Runs() {
		if r.State == state.Succeeded && !b.done[r.ID] {
			b.done[r.ID] = true
			b.kv.Set("fulfil:"+b.orders[r.ID], "done", 0)
		}
	}
	return nil
}

func (b *Bridge) Fulfilment(order string) (state.State, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, o := range b.orders {
		if o != order {
			continue
		}
		r, err := b.eng.Get(id)
		if err != nil {
			return state.None, false
		}
		return r.State, true
	}
	return state.None, false
}

func (b *Bridge) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

func (b *Bridge) Store() *store.Store { return b.kv }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
