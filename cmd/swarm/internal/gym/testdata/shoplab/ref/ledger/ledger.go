package ledger

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"shoplab/errs"
	"shoplab/money"
)

type Posting struct {
	Account string
	Amount  money.Money
}

type Txn struct {
	ID       int64
	AtMs     int64
	Memo     string
	Postings []Posting
}

type Ledger struct {
	mu   sync.Mutex
	now  func() time.Time
	txns []Txn
	bal  map[string]money.Money
}

func New(now func() time.Time) *Ledger { return &Ledger{now: now, bal: map[string]money.Money{}} }

func (l *Ledger) Post(memo string, ps ...Posting) (int64, error) {
	if len(ps) < 2 {
		return 0, fmt.Errorf("ledger: %d postings: %w", len(ps), errs.ErrInvalid)
	}
	var sum money.Money
	for _, p := range ps {
		if p.Account == "" || p.Amount == 0 {
			return 0, fmt.Errorf("ledger: bad posting %+v: %w", p, errs.ErrInvalid)
		}
		sum += p.Amount
	}
	if sum != 0 {
		return 0, fmt.Errorf("ledger: unbalanced by %s: %w", sum, errs.ErrInvalid)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t := Txn{ID: int64(len(l.txns)) + 1, AtMs: l.now().UnixMilli(), Memo: memo, Postings: append([]Posting(nil), ps...)}
	l.txns = append(l.txns, t)
	for _, p := range ps {
		l.bal[p.Account] += p.Amount
	}
	return t.ID, nil
}

func (l *Ledger) Balance(account string) money.Money {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bal[account]
}

func (l *Ledger) Accounts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.bal))
	for a := range l.bal {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func (l *Ledger) Txns() []Txn {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Txn, len(l.txns))
	for i, t := range l.txns {
		t.Postings = append([]Posting(nil), t.Postings...)
		out[i] = t
	}
	return out
}

func (l *Ledger) TrialBalance() money.Money {
	l.mu.Lock()
	defer l.mu.Unlock()
	var sum money.Money
	for _, b := range l.bal {
		sum += b
	}
	return sum
}
