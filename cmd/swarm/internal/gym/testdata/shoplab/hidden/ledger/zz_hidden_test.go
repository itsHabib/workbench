package ledger

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"shoplab/errs"
)

func TestHiddenPostAndBalances(t *testing.T) {
	now := time.UnixMilli(7000)
	l := New(func() time.Time { return now })
	id, err := l.Post("sale", Posting{"cash", 2118}, Posting{"revenue", -1355}, Posting{"tax", -113}, Posting{"shipping", -650})
	if err != nil || id != 1 {
		t.Fatalf("%d %v", id, err)
	}
	now = now.Add(time.Second)
	id, err = l.Post("refund", Posting{"revenue", 355}, Posting{"cash", -355})
	if err != nil || id != 2 {
		t.Fatalf("%d %v", id, err)
	}
	if l.Balance("cash") != 1763 || l.Balance("revenue") != -1000 || l.Balance("tax") != -113 || l.Balance("nope") != 0 {
		t.Fatalf("cash %d revenue %d tax %d", l.Balance("cash"), l.Balance("revenue"), l.Balance("tax"))
	}
	if !reflect.DeepEqual(l.Accounts(), []string{"cash", "revenue", "shipping", "tax"}) {
		t.Fatalf("%v", l.Accounts())
	}
	if l.TrialBalance() != 0 {
		t.Fatalf("trial balance %d", l.TrialBalance())
	}
	txns := l.Txns()
	if len(txns) != 2 || txns[0].ID != 1 || txns[0].AtMs != 7000 || txns[0].Memo != "sale" || len(txns[0].Postings) != 4 ||
		txns[1].AtMs != 8000 || txns[1].Postings[1] != (Posting{"cash", -355}) {
		t.Fatalf("%+v", txns)
	}
}

func TestHiddenPostRejects(t *testing.T) {
	l := New(time.Now)
	for name, ps := range map[string][]Posting{
		"none":       nil,
		"one":        {{"cash", 0}},
		"unbalanced": {{"cash", 100}, {"revenue", -99}},
		"zero":       {{"cash", 100}, {"revenue", -100}, {"tax", 0}},
		"no account": {{"", 100}, {"revenue", -100}},
	} {
		if id, err := l.Post(name, ps...); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: %d %v", name, id, err)
		}
	}
	if len(l.Txns()) != 0 || len(l.Accounts()) != 0 || l.Balance("cash") != 0 {
		t.Fatal("a failed post left a trace")
	}
	if id, err := l.Post("ok", Posting{"cash", 1}, Posting{"revenue", -1}); err != nil || id != 1 {
		t.Fatalf("failed posts used ids: %d %v", id, err)
	}
}

func TestHiddenSameAccountTwice(t *testing.T) {
	l := New(time.Now)
	if _, err := l.Post("move", Posting{"cash", 300}, Posting{"cash", -100}, Posting{"bank", -200}); err != nil {
		t.Fatal(err)
	}
	if l.Balance("cash") != 200 || l.Balance("bank") != -200 {
		t.Fatalf("cash %d bank %d", l.Balance("cash"), l.Balance("bank"))
	}
	_, _ = l.Post("back", Posting{"bank", 200}, Posting{"cash", -200})
	if !reflect.DeepEqual(l.Accounts(), []string{"bank", "cash"}) || l.Balance("cash") != 0 {
		t.Fatalf("zeroed accounts must stay listed: %v", l.Accounts())
	}
}

func TestHiddenTxnsAreCopies(t *testing.T) {
	l := New(time.Now)
	ps := []Posting{{"cash", 100}, {"revenue", -100}}
	_, _ = l.Post("sale", ps...)
	ps[0].Amount = 999
	got := l.Txns()
	if got[0].Postings[0].Amount != 100 {
		t.Fatal("ledger kept the caller's slice")
	}
	got[0].Postings[0].Amount = 5
	got[0].Memo = "scribble"
	if again := l.Txns(); again[0].Postings[0].Amount != 100 || again[0].Memo != "sale" {
		t.Fatal("Txns handed out the ledger's own data")
	}
}

func TestHiddenConcurrentPosts(t *testing.T) {
	l := New(time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Post("sale", Posting{"cash", 250}, Posting{"revenue", -250}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if l.Balance("cash") != 10000 || l.Balance("revenue") != -10000 || l.TrialBalance() != 0 {
		t.Fatalf("cash %d revenue %d", l.Balance("cash"), l.Balance("revenue"))
	}
	seen := map[int64]bool{}
	for _, tx := range l.Txns() {
		seen[tx.ID] = true
	}
	for id := int64(1); id <= 40; id++ {
		if !seen[id] {
			t.Fatalf("missing txn id %d", id)
		}
	}
}
