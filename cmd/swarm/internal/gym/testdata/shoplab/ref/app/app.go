package app

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"shoplab/cart"
	"shoplab/catalog"
	"shoplab/command"
	"shoplab/discount"
	"shoplab/errs"
	"shoplab/idem"
	"shoplab/inventory"
	"shoplab/ledger"
	"shoplab/money"
	"shoplab/order"
	"shoplab/outbox"
	"shoplab/pricing"
	"shoplab/report"
	"shoplab/shipping"
	"shoplab/sku"
)

const (
	holdFor = 15 * time.Minute
	idemFor = 24 * time.Hour
)

type App struct {
	mu      sync.Mutex
	cat     *catalog.Catalog
	inv     *inventory.Inventory
	coupons *discount.Book
	books   *ledger.Ledger
	orders  *order.Book
	idem    *idem.Store
	log     *outbox.Log
	calc    *pricing.Calc
	carts   map[string]*cart.Cart
	holds   map[string]string // order id -> reservation id
	nextRes int
}

func New(now func() time.Time, events io.Writer) *App {
	calc, _ := pricing.New(map[string]int64{"general": 1000, "food": 500, "book": 0})
	return &App{
		cat: catalog.New(), inv: inventory.New(now), coupons: discount.New(now), books: ledger.New(now),
		orders: order.NewBook(now), idem: idem.New(now, idemFor), log: outbox.NewLog(now, events), calc: calc,
		carts: map[string]*cart.Cart{}, holds: map[string]string{},
	}
}

func (a *App) Events() []outbox.Event { return a.log.Events() }

func (a *App) Exec(line string) (string, error) {
	cmd, err := command.Parse(line)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	reply, _, err := a.idem.Do(cmd.IdemKey, func() (string, error) { return a.run(cmd) })
	if err != nil {
		return "", err
	}
	return reply, nil
}

func atoi(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("app: number %q: %w", s, errs.ErrInvalid)
	}
	return n, nil
}

func (a *App) run(c command.Cmd) (string, error) {
	switch c.Op {
	case "PRODUCT":
		return a.product(c.Args)
	case "STOCK":
		return a.stock(c.Args)
	case "AVAIL":
		p, err := a.known(c.Args[0])
		if err != nil {
			return "", err
		}
		return strconv.Itoa(a.inv.Available(p.SKU)), nil
	case "COUPON":
		return a.coupon(c.Args)
	case "ADD":
		return a.add(c.Args)
	case "REMOVE":
		return a.remove(c.Args)
	case "SHOW":
		return a.show(c.Args[0])
	case "CHECKOUT":
		return a.checkout(c.Args)
	case "PAY":
		return a.pay(c.Args)
	case "SHIP":
		return a.advance(c.Args[0], a.orders.Ship, "SHIPPED", "order.shipped")
	case "DELIVER":
		return a.advance(c.Args[0], a.orders.Deliver, "DELIVERED", "order.delivered")
	case "CANCEL":
		return a.cancel(c.Args[0])
	case "ORDER":
		o, err := a.orders.Get(c.Args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s", o.State, o.Quote.Total), nil
	case "BALANCE":
		return a.books.Balance(c.Args[0]).String(), nil
	case "REPORT":
		return report.Summarize(a.orders.List()).String(), nil
	}
	return "", fmt.Errorf("app: op %q: %w", c.Op, errs.ErrInvalid)
}

// known parses a SKU and requires it to be in the catalog.
func (a *App) known(s string) (catalog.Product, error) {
	id, err := sku.Parse(s)
	if err != nil {
		return catalog.Product{}, err
	}
	return a.cat.Get(id)
}

func (a *App) product(args []string) (string, error) {
	id, err := sku.Parse(args[0])
	if err != nil {
		return "", err
	}
	price, err := money.Parse(args[2])
	if err != nil {
		return "", err
	}
	grams, err := atoi(args[3])
	if err != nil {
		return "", err
	}
	err = a.cat.Put(catalog.Product{SKU: id, Name: args[1], Price: price, WeightG: grams, Category: args[4]})
	if err != nil {
		return "", err
	}
	return "OK", nil
}

func (a *App) stock(args []string) (string, error) {
	p, err := a.known(args[0])
	if err != nil {
		return "", err
	}
	qty, err := atoi(args[1])
	if err != nil {
		return "", err
	}
	if err := a.inv.Receive(p.SKU, qty); err != nil {
		return "", err
	}
	return strconv.Itoa(a.inv.Available(p.SKU)), nil
}

func (a *App) coupon(args []string) (string, error) {
	c := discount.Coupon{Code: args[0]}
	var err error
	switch strings.ToUpper(args[1]) {
	case "PCT":
		var bp int
		bp, err = atoi(args[2])
		c.PercentBP = int64(bp)
	case "FIXED":
		c.Fixed, err = money.Parse(args[2])
	default:
		err = fmt.Errorf("app: coupon kind %q: %w", args[1], errs.ErrInvalid)
	}
	if err != nil {
		return "", err
	}
	if len(args) == 4 {
		if c.MinSubtotal, err = money.Parse(args[3]); err != nil {
			return "", err
		}
	}
	if err := a.coupons.Add(c); err != nil {
		return "", err
	}
	return "OK", nil
}

func (a *App) add(args []string) (string, error) {
	id, err := sku.Parse(args[1])
	if err != nil {
		return "", err
	}
	qty, err := atoi(args[2])
	if err != nil {
		return "", err
	}
	c, ok := a.carts[args[0]]
	if !ok {
		c = cart.New(args[0], a.cat)
	}
	if err := c.Add(id, qty); err != nil {
		return "", err
	}
	sub, err := c.Subtotal()
	if err != nil {
		return "", err
	}
	a.carts[args[0]] = c
	return sub.String(), nil
}

func (a *App) cart(id string) (*cart.Cart, error) {
	c, ok := a.carts[id]
	if !ok {
		return nil, fmt.Errorf("app: cart %q: %w", id, errs.ErrNotFound)
	}
	return c, nil
}

func (a *App) remove(args []string) (string, error) {
	c, err := a.cart(args[0])
	if err != nil {
		return "", err
	}
	id, err := sku.Parse(args[1])
	if err != nil {
		return "", err
	}
	if err := c.Remove(id); err != nil {
		return "", err
	}
	sub, err := c.Subtotal()
	if err != nil {
		return "", err
	}
	return sub.String(), nil
}

func (a *App) show(id string) (string, error) {
	c, err := a.cart(id)
	if err != nil {
		return "", err
	}
	lines, err := c.Lines()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var sub money.Money
	for _, l := range lines {
		fmt.Fprintf(&b, "%s %d %s\n", l.SKU, l.Qty, l.Total)
		sub += l.Total
	}
	fmt.Fprintf(&b, "subtotal %s", sub)
	return b.String(), nil
}

func (a *App) emit(kind, orderID string, more ...string) {
	f := map[string]string{"order": orderID}
	for i := 0; i+1 < len(more); i += 2 {
		f[more[i]] = more[i+1]
	}
	_, _ = a.log.Append(kind, f)
}

func (a *App) checkout(args []string) (string, error) {
	c, err := a.cart(args[0])
	if err != nil {
		return "", err
	}
	zone, err := shipping.ParseZone(args[1])
	if err != nil {
		return "", err
	}
	lines, err := c.Lines()
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("app: cart %q is empty: %w", args[0], errs.ErrInvalid)
	}
	var sub, off money.Money
	olines := make([]order.Line, len(lines))
	for i, l := range lines {
		sub += l.Total
		olines[i] = order.Line{SKU: l.SKU, Qty: l.Qty, Total: l.Total}
	}
	if len(args) == 3 {
		if off, err = a.coupons.Quote(args[2], sub); err != nil {
			return "", err
		}
	}
	q, err := a.calc.Quote(lines, off, zone)
	if err != nil {
		return "", err
	}
	a.nextRes++
	res := fmt.Sprintf("R-%d", a.nextRes)
	if err := a.inv.Reserve(res, c.Items(), holdFor); err != nil {
		return "", err
	}
	if len(args) == 3 {
		if _, err := a.coupons.Redeem(args[2], sub); err != nil {
			_ = a.inv.Release(res)
			return "", err
		}
	}
	o, err := a.orders.Create(olines, q)
	if err != nil {
		_ = a.inv.Release(res)
		return "", err
	}
	a.holds[o.ID] = res
	delete(a.carts, args[0])
	a.emit("order.created", o.ID, "total", q.Total.String())
	return fmt.Sprintf("%s %s", o.ID, q.Total), nil
}

func payment(q pricing.Quote, sign money.Money) []ledger.Posting {
	var ps []ledger.Posting
	for _, p := range []ledger.Posting{
		{Account: "cash", Amount: q.Total},
		{Account: "revenue", Amount: -(q.Subtotal - q.Discount)},
		{Account: "tax", Amount: -q.Tax},
		{Account: "shipping", Amount: -q.Shipping},
	} {
		if p.Amount != 0 {
			p.Amount *= sign
			ps = append(ps, p)
		}
	}
	return ps
}

func (a *App) post(memo string, ps []ledger.Posting) error {
	if len(ps) == 0 {
		return nil
	}
	_, err := a.books.Post(memo, ps...)
	return err
}

func (a *App) pay(args []string) (string, error) {
	o, err := a.orders.Get(args[0])
	if err != nil {
		return "", err
	}
	amount, err := money.Parse(args[1])
	if o.State != order.Pending {
		return "", fmt.Errorf("app: order %s is %s: %w", o.ID, o.State, errs.ErrState)
	}
	if err != nil {
		return "", err
	}
	if amount != o.Quote.Total {
		return "", fmt.Errorf("app: order %s costs %s: %w", o.ID, o.Quote.Total, errs.ErrInvalid)
	}
	if err := a.inv.Commit(a.holds[o.ID]); err != nil {
		if !errors.Is(err, errs.ErrNotFound) {
			return "", err
		}
		if _, err := a.orders.Cancel(o.ID); err != nil {
			return "", err
		}
		a.emit("order.cancelled", o.ID)
		return "", fmt.Errorf("app: order %s lost its reservation: %w", o.ID, errs.ErrState)
	}
	if err := a.orders.Pay(o.ID, amount); err != nil {
		return "", err
	}
	if err := a.post("pay "+o.ID, payment(o.Quote, 1)); err != nil {
		return "", err
	}
	a.emit("order.paid", o.ID)
	return "PAID", nil
}

func (a *App) advance(id string, move func(string) error, reply, kind string) (string, error) {
	if err := move(id); err != nil {
		return "", err
	}
	a.emit(kind, id)
	return reply, nil
}

func (a *App) cancel(id string) (string, error) {
	o, err := a.orders.Get(id)
	if err != nil {
		return "", err
	}
	st, err := a.orders.Cancel(id)
	if err != nil {
		return "", err
	}
	if st == order.Cancelled {
		_ = a.inv.Release(a.holds[id])
		a.emit("order.cancelled", id)
		return "CANCELLED", nil
	}
	if err := a.post("refund "+id, payment(o.Quote, -1)); err != nil {
		return "", err
	}
	for _, l := range o.Lines {
		_ = a.inv.Receive(l.SKU, l.Qty)
	}
	a.emit("order.refunded", id)
	return "REFUNDED", nil
}
