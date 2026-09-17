# shoplab

Build a small order-processing backend as a Go module named `shoplab` (go 1.22, standard library only).
Sixteen packages in layers; a package imports only the packages its section names. Hidden tests exercise
each package and the whole app, so the names and behaviour below are exact.

Layers, lowest first: `errs` / `money`, `sku`, `idem`, `outbox` / `catalog`, `inventory`, `shipping`,
`discount`, `ledger`, `command` / `cart` / `pricing` / `order` / `report` / `app`.

## Shared decisions
- Every error this spec names a sentinel for satisfies `errors.Is(err, thatSentinel)`. Wrapping with more text is fine. Errors passed up from a lower package keep their sentinel.
- All amounts are `money.Money`, an integer count of cents. No floats anywhere. Rounding is always half away from zero, and only `money` rounds.
- All SKUs are `sku.SKU` in canonical (upper-case) form.
- Clocks are injected as `now func() time.Time`. Nothing reads the wall clock. A thing with a ttl expires when `now()` is at or past its start time plus ttl; a ttl of 0 never expires. Timestamps stored in records are `now().UnixMilli()`.
- When an operation returns an error, it changed nothing, unless this spec says otherwise.
- "Safe for concurrent use" means correct under `go test -race`.

## errs
- Sentinels, all distinct, made with `errors.New`: `ErrInvalid`, `ErrNotFound`, `ErrConflict`, `ErrInsufficient`, `ErrState`.
- `func Code(err error) string`. nil gives `OK`. Otherwise the first sentinel, in the order listed above, that `errors.Is` matches gives `INVALID`, `NOT_FOUND`, `CONFLICT`, `INSUFFICIENT`, `STATE`. Anything else gives `INTERNAL`.

## money (imports errs)
- `type Money int64`. Cents.
- `func Parse(s string) (Money, error)`. Accepts an optional leading `-`, one or more digits, then optionally `.` and one or two digits: `12` is 1200, `12.3` is 1230, `-0.05` is -5. Anything else (empty, spaces, `+1`, `.5`, `1.`, `1.234`, `1,00`, letters) is `ErrInvalid`.
- `(Money) String() string`. Always two decimals, sign first: `12.34`, `0.00`, `-0.05`, `1200.00`. `Parse(m.String()) == m`.
- `(Money) Mul(qty int) Money`. Exact product.
- `(Money) BP(bp int64) Money`. The fraction bp/10000 of m (basis points: 1000 is 10%), rounded half away from zero: `Money(1005).BP(1000)` is 101, `Money(-1005).BP(1000)` is -101, `Money(1004).BP(1000)` is 100.
- `(Money) Div(n int64) (Money, error)`. m/n rounded half away from zero: 100/3 is 33, 200/3 is 67, -5/2 is -3. n <= 0 is `ErrInvalid`.
- `func Sum(ms ...Money) Money`. 0 for none.
- `func Allocate(total Money, weights []int64) ([]Money, error)`. Splits total in proportion to weights so the parts add up to total exactly. Each part starts as the floor of its exact share `total*w/sum(weights)`; the leftover cents go one each to the parts with the largest remainder, ties to the lower index. `Allocate(100, {1,1,1})` is `{34,33,33}`; `Allocate(5, {3,7})` is `{2,3}`; `Allocate(150, {1000,505})` is `{100,50}`; a zero weight gets 0. `ErrInvalid` when weights is empty, any weight is negative, all weights are zero, or total is negative. Must not modify weights.

## sku (imports errs)
- `type SKU string`.
- `func Parse(s string) (SKU, error)`. Trims surrounding white space and upper-cases. Valid after that: 3 to 16 characters from `A-Z`, `0-9`, `-`, with the first and last character not `-`. Otherwise `ErrInvalid`.
- `func MustParse(s string) SKU`. Panics where Parse errors.
- `(SKU) String() string`. `func Sort(s []SKU)` sorts ascending in place.

## idem (imports nothing from the module)
- `func New(now func() time.Time, ttl time.Duration) *Store`.
- `(*Store) Do(key string, fn func() (string, error)) (reply string, replayed bool, err error)`.
  If key has a live record: returns the recorded reply, `true`, nil and does not call fn. Otherwise calls fn and returns its reply and error with `false`; when fn's error is nil the reply is recorded at `now()`. A failed fn records nothing, so the key can be retried. An empty key always calls fn and never records.
- fn runs while the store is locked: concurrent Do calls with one key call fn exactly once (when it succeeds). fn must not call the same store.
- `(*Store) Len() int`. Count of live records. Safe for concurrent use.

## outbox (imports errs)
- `type Event struct { Seq int64; AtMs int64; Kind string; Fields map[string]string }`.
- `func Encode(e Event) string`. One line, no newline in the output, whatever valid UTF-8 the kind, keys and values hold (spaces, tabs, newlines, quotes, unicode). Deterministic: equal events encode to equal strings.
- `func Decode(line string) (Event, error)`. `Decode(Encode(e))` equals e (`reflect.DeepEqual`), except that nil Fields come back as an empty non-nil map. Garbage, trailing garbage after an event, or an empty Kind is `ErrInvalid`.
- `func ReadAll(r io.Reader) ([]Event, error)`. Decodes each line in order, skips blank lines. A bad line, or a Seq not greater than the previous event's Seq, stops with `ErrInvalid` whose text contains `line N` (1-based, counting blank lines).
- `func NewLog(now func() time.Time, w io.Writer) *Log`. `w` may be nil.
- `(*Log) Append(kind string, fields map[string]string) (Event, error)`. Empty kind is `ErrInvalid`. Seq starts at 1 and rises by 1; AtMs is `now().UnixMilli()`; fields are copied, so the caller changing its map later changes nothing in the log. A nil map gives an event with empty, non-nil Fields. Writes `Encode(event) + "\n"` to `w`; lines reach `w` in Seq order even under concurrent Appends.
- `(*Log) Events() []Event`. All events in order. `(*Log) Since(seq int64) []Event`. Events with Seq greater than seq. Both return copies. Safe for concurrent use.

## catalog (imports errs, money, sku)
- `type Product struct { SKU sku.SKU; Name string; Price money.Money; WeightG int; Category string }`. WeightG is grams per unit.
- `func New() *Catalog`.
- `(*Catalog) Put(p Product) error`. Adds or replaces. `ErrInvalid` when `sku.Parse(string(p.SKU))` fails or does not give back p.SKU unchanged, Name or Category is empty, Price or WeightG is negative.
- `(*Catalog) Get(s sku.SKU) (Product, error)` and `(*Catalog) Remove(s sku.SKU) error`. `ErrNotFound` when absent.
- `(*Catalog) List() []Product`. Sorted by SKU ascending. `(*Catalog) Len() int`. Safe for concurrent use.

## inventory (imports errs, sku)
- `func New(now func() time.Time) *Inventory`.
- `(*Inventory) Receive(s sku.SKU, qty int) error`. Adds to on-hand. qty <= 0 is `ErrInvalid`.
- `(*Inventory) OnHand(s sku.SKU) int`. `(*Inventory) Available(s sku.SKU) int`. On-hand minus live reservations. Both 0 for an unknown SKU.
- `(*Inventory) Reserve(id string, items map[sku.SKU]int, ttl time.Duration) error`. All or nothing. `ErrInvalid` for an empty id, no items, or any qty <= 0. `ErrConflict` when id already has a live reservation. `ErrInsufficient` when any qty exceeds Available. An expired reservation is gone: its stock is available again and its id can be reused. Items are copied.
- `(*Inventory) Release(id string) error`. Drops a live reservation. `(*Inventory) Commit(id string) error`. Drops a live reservation and takes its quantities off on-hand. Both `ErrNotFound` when id has no live reservation.
- `(*Inventory) Reserved(id string) (map[sku.SKU]int, bool)`. A copy of a live reservation's items.
- Safe for concurrent use: concurrent Reserve calls never oversell.

## shipping (imports errs, money)
- `type Zone string` with `Domestic Zone = "DOM"` and `International Zone = "INTL"`.
- `func ParseZone(s string) (Zone, error)`. Case-insensitive `dom` or `intl`; otherwise `ErrInvalid`.
- `func Rate(z Zone, weightG int, subtotal money.Money) (money.Money, error)`.
  `ErrInvalid` for an unknown zone, negative weight, weight over 30000, or negative subtotal; these are checked before anything else, so an overweight parcel is an error even when it would ship free. Weight 0 costs 0.
  Domestic: 5.00 covers the first 1000 g, plus 1.50 for every started 500 g beyond that (1000 g is 5.00, 1001 g and 1500 g are 6.50, 1501 g is 8.00). Free when subtotal is 50.00 or more.
  International: 15.00 covers the first 1000 g, plus 4.00 for every started 500 g beyond. Never free.

## discount (imports errs, money)
- `type Coupon struct { Code string; PercentBP int64; Fixed money.Money; MinSubtotal money.Money; Expires time.Time; MaxUses int }`. Exactly one of PercentBP and Fixed is non-zero. A zero Expires never expires; MaxUses 0 is unlimited.
- `func New(now func() time.Time) *Book`.
- `(*Book) Add(c Coupon) error`. Codes are case-insensitive (stored upper-case). `ErrInvalid` for an empty code, both or neither of PercentBP and Fixed set, PercentBP outside 1..10000, or a negative Fixed, MinSubtotal or MaxUses. `ErrConflict` when the code exists.
- `(*Book) Quote(code string, subtotal money.Money) (money.Money, error)`. The amount off, consuming nothing. Checked in this order: unknown code `ErrNotFound`; expired (`now()` at or past Expires) `ErrState`; uses exhausted `ErrState`; subtotal negative or below MinSubtotal `ErrInvalid`. Percent coupons give `subtotal.BP(PercentBP)`; fixed coupons give Fixed, capped at subtotal.
- `(*Book) Redeem(code string, subtotal money.Money) (money.Money, error)`. Same as Quote and counts one use on success.
- `(*Book) Uses(code string) int`. 0 for an unknown code. Safe for concurrent use: a coupon with MaxUses n is redeemed at most n times.

## ledger (imports errs, money)
- Double entry. `type Posting struct { Account string; Amount money.Money }`. Positive is a debit, negative a credit.
- `type Txn struct { ID int64; AtMs int64; Memo string; Postings []Posting }`.
- `func New(now func() time.Time) *Ledger`.
- `(*Ledger) Post(memo string, ps ...Posting) (int64, error)`. Records a transaction and returns its ID (1, 2, 3, ...). `ErrInvalid` when there are fewer than two postings, an account is empty, an amount is zero, or the amounts do not add up to zero. A failed Post uses up no ID. Postings are copied, so the caller changing its slice later changes nothing in the ledger.
- `(*Ledger) Balance(account string) money.Money`. Sum of the account's amounts, 0 if unknown.
- `(*Ledger) Accounts() []string`. Every account ever posted to, sorted. `(*Ledger) Txns() []Txn`. In order; deep copies.
- `(*Ledger) TrialBalance() money.Money`. Sum of all balances; always 0. Safe for concurrent use.

## command (imports errs)
- `type Cmd struct { Op string; Args []string; IdemKey string }`. Op is upper case. Args are verbatim (case kept) and non-nil.
- `func Parse(line string) (Cmd, error)`. Every failure is `ErrInvalid`.
- Tokens are separated by spaces or tabs. A token starting with `"` is quoted: it runs to the closing `"`, may hold spaces and tabs, `\"` is a quote and `\\` a backslash, any other backslash sequence is an error, and the closing quote must be followed by white space or the end. `""` is an empty argument. An unterminated quote is an error, and so is a `"` inside an unquoted token.
- An unquoted token starting with `@` is the idempotency key (the text after `@`), wherever it appears. Two keys, or a bare `@`, are errors. The first remaining token is the op, case-insensitive; the rest are Args.
- Ops and how many Args each takes: `PRODUCT` 5, `STOCK` 2, `AVAIL` 1, `COUPON` 3 or 4, `ADD` 3, `REMOVE` 2, `SHOW` 1, `CHECKOUT` 2 or 3, `PAY` 2, `SHIP` 1, `DELIVER` 1, `CANCEL` 1, `ORDER` 1, `BALANCE` 1, `REPORT` 0. An unknown op, a wrong count, or an empty or blank line (or one holding only a key) is an error. Parse checks nothing else about the Args.
- `func Ops() []string`. The fifteen ops, sorted.

## cart (imports errs, money, sku, catalog)
- `func New(id string, cat *catalog.Catalog) *Cart`. `(*Cart) ID() string`. A Cart is not safe for concurrent use.
- `(*Cart) Add(s sku.SKU, qty int) error`. Adds to the SKU's line. qty <= 0 is `ErrInvalid`; a SKU absent from the catalog is `ErrNotFound`; a line may hold at most 999, going over is `ErrInvalid`.
- `(*Cart) Set(s sku.SKU, qty int) error`. Sets the line's quantity; 0 removes the line (no error if there was none). Negative or over 999 is `ErrInvalid`; a SKU absent from the catalog is `ErrNotFound`.
- `(*Cart) Remove(s sku.SKU) error`. `ErrNotFound` when the cart has no such line; a line can be removed even after its product has left the catalog. `(*Cart) Clear()` drops every line.
- `type Line struct { SKU sku.SKU; Name string; Qty int; Unit, Total money.Money; WeightG int; Category string }`. Total is `Unit.Mul(Qty)`; WeightG is the whole line's weight.
- `(*Cart) Lines() ([]Line, error)`. Sorted by SKU. Name, price, weight and category are read from the catalog at the time of the call, not when the item was added. A line whose product has left the catalog makes it `ErrNotFound`.
- `(*Cart) Subtotal() (money.Money, error)` and `(*Cart) WeightG() (int, error)`. Sums over Lines, same error.
- `(*Cart) Items() map[sku.SKU]int`. A copy, non-nil. `(*Cart) Len() int`. Number of lines.

## pricing (imports errs, money, shipping, cart)
- `type Quote struct { Subtotal, Discount, Tax, Shipping, Total money.Money }`.
- `func New(taxBP map[string]int64) (*Calc, error)`. Tax rate in basis points per product category. A rate outside 0..10000 is `ErrInvalid`. The map is copied.
- `(*Calc) Quote(lines []cart.Line, discount money.Money, zone shipping.Zone) (Quote, error)`.
  - Subtotal is the sum of line Totals. `ErrInvalid` when there are no lines, or discount is negative or more than Subtotal.
  - A non-zero discount is spread over the lines with `money.Allocate(discount, line Totals)` in the order given.
  - Tax is worked out per line and then summed: `(line Total - line's discount share).BP(rate of the line's Category)`. A category with no rate is untaxed. International orders pay no tax at all.
  - Shipping is `shipping.Rate(zone, sum of line WeightG, Subtotal - Discount)`; its errors pass through.
  - Total is Subtotal - Discount + Tax + Shipping.
  - Example: rates `general` 1000 and `food` 500; lines 10.00 general 600 g and 5.05 food 600 g; discount 1.50; domestic. Shares are 1.00 and 0.50, tax is 0.90 + 0.23 = 1.13, shipping 6.50, total 21.18.

## order (imports errs, money, sku, pricing)
- `type State string`: `Pending = "PENDING"`, `Paid = "PAID"`, `Shipped = "SHIPPED"`, `Delivered = "DELIVERED"`, `Cancelled = "CANCELLED"`, `Refunded = "REFUNDED"`.
- `func CanTransition(from, to State) bool`. True only for Pending to Paid, Pending to Cancelled, Paid to Shipped, Paid to Refunded, Shipped to Delivered.
- `type Line struct { SKU sku.SKU; Qty int; Total money.Money }`.
- `type Order struct { ID string; State State; Lines []Line; Quote pricing.Quote; CreatedMs, UpdatedMs int64 }`.
- `func NewBook(now func() time.Time) *Book`.
- `(*Book) Create(lines []Line, q pricing.Quote) (Order, error)`. IDs are `O-1`, `O-2`, ... State Pending, CreatedMs and UpdatedMs set from `now()`. `ErrInvalid` when there are no lines, any Qty <= 0, or `q.Total` is negative; a failed Create uses up no ID. Lines are copied.
- `(*Book) Get(id string) (Order, error)`. `(*Book) List() []Order`. In creation order. Orders returned by Create, Get and List are copies that share no Lines storage with the book.
- `(*Book) Pay(id string, amount money.Money) error`. Pending to Paid. amount must equal `Quote.Total`, else `ErrInvalid`. The state is checked before the amount.
- `(*Book) Ship(id string) error`. Paid to Shipped. `(*Book) Deliver(id string) error`. Shipped to Delivered.
- `(*Book) Cancel(id string) (State, error)`. Pending becomes Cancelled, Paid becomes Refunded; returns the new state.
- In all of these an unknown id is `ErrNotFound` and a transition from the wrong state is `ErrState`. Every successful transition sets UpdatedMs. Safe for concurrent use.

## report (imports money, sku, order)
- `type Summary struct { Orders int; ByState map[order.State]int; Gross, Refunded, Net, AOV money.Money; Units map[sku.SKU]int }`.
- `func Summarize(orders []order.Order) Summary`.
  - Orders is the count of all orders. ByState counts them per state, holding only states that occur. Both maps are non-nil even for no orders.
  - Gross is the sum of `Quote.Total` over orders that are Paid, Shipped, Delivered or Refunded. Refunded is that sum over Refunded orders. Net is Gross - Refunded.
  - Units adds up line Qty per SKU over orders that are Paid, Shipped or Delivered.
  - AOV is Net divided by the number of Paid, Shipped or Delivered orders (`money.Div`), 0 when there are none.
- `(Summary) Top(n int) []sku.SKU`. SKUs by Units descending, ties by SKU ascending, at most n. Empty for n <= 0.
- `(Summary) String() string`. `orders=3 gross=42.36 refunded=21.18 net=21.18 aov=21.18`.

## app (imports everything it needs)
- `func New(now func() time.Time, events io.Writer) *App`. `events` may be nil. The app owns one catalog, inventory, discount book, ledger, order book, idem store (ttl 24 hours), outbox log (writing to `events`) and a pricing Calc with rates `general` 1000, `food` 500, `book` 0. All share `now`.
- `(*App) Exec(line string) (string, error)`. Parses with `command`, runs the command, returns the reply. On error the reply is empty. Exec calls are serialized; safe for concurrent use.
- `(*App) Events() []outbox.Event`. The outbox log's events.
- Arguments: SKUs go through `sku.Parse`, amounts through `money.Parse`, quantities, weights and basis points are decimal integers, zones go through `shipping.ParseZone`. A malformed argument is `ErrInvalid`. Cart ids, order ids, coupon codes and account names are taken as given.
- Commands and replies:
  - `PRODUCT sku name price weightG category` puts a product. Reply `OK`.
  - `STOCK sku qty` receives stock. The SKU must be in the catalog (`ErrNotFound`). Reply is the decimal Available afterwards.
  - `AVAIL sku`. The decimal Available. SKU not in the catalog is `ErrNotFound`.
  - `COUPON code PCT bp [min]` or `COUPON code FIXED amount [min]` adds a coupon with optional MinSubtotal, no expiry, unlimited uses. `PCT`/`FIXED` is case-insensitive. Reply `OK`.
  - `ADD cart sku qty`. A cart comes into being with its first successful ADD. Reply is the cart's subtotal, such as `15.05`.
  - `REMOVE cart sku`. Reply is the subtotal afterwards. An unknown cart or line is `ErrNotFound`. A cart emptied this way still exists.
  - `SHOW cart`. One line `SKU qty total` per cart line in SKU order, then `subtotal X`, joined by `\n`. For an empty cart just `subtotal 0.00`. Unknown cart is `ErrNotFound`.
  - `CHECKOUT cart zone [coupon]`. Checked in this order: unknown cart `ErrNotFound`; bad zone `ErrInvalid`; empty cart `ErrInvalid`; coupon errors from `discount.Quote`; pricing errors; stock `ErrInsufficient`. The discount is `discount.Quote(coupon, subtotal)` (0 without a coupon), the quote comes from pricing, the stock is reserved with `inventory.Reserve` for 15 minutes, and only when all of that has worked is the coupon redeemed, the order created and the cart deleted (its id is free to use again). On any error the cart, the stock and the coupon are untouched and no order id is used up. Reply is `<orderID> <total>`, such as `O-1 21.18`.
  - `PAY order amount`. Checked in this order: unknown order `ErrNotFound`; order not Pending `ErrState`; amount malformed or not the order total `ErrInvalid`; reservation expired: the order is cancelled (this does happen despite the error) and the error is `ErrState`. Otherwise the reservation is committed, the order is Paid and the ledger gets one transaction: debit `cash` Total, credit `revenue` Subtotal - Discount, credit `tax` Tax, credit `shipping` Shipping, leaving out zero amounts (nothing is posted when Total is 0). Reply `PAID`.
  - `SHIP order` and `DELIVER order`. Replies `SHIPPED` and `DELIVERED`.
  - `CANCEL order`. A Pending order releases its reservation if that is still live; reply `CANCELLED`. A Paid order is refunded: the ledger gets the exact reverse of the payment transaction and the quantities go back into stock (`inventory.Receive`); reply `REFUNDED`.
  - `ORDER order`. Reply `<STATE> <total>`, such as `PAID 21.18`.
  - `BALANCE account`. The ledger balance, such as `21.18` for `cash` and `-13.55` for `revenue`.
  - `REPORT`. `report.Summarize` over all orders, as its String.
- Events, appended only when the command succeeded, in this order of kinds where several apply: `order.created` with fields `order` and `total`; `order.paid`, `order.shipped`, `order.delivered`, `order.cancelled`, `order.refunded`, each with the field `order`. The cancellation forced by a PAY on an expired reservation also appends `order.cancelled`. No other events.
- Idempotency: a command carrying `@key` runs through `idem.Do`. Repeating a key within 24 hours returns the first successful reply without running anything again, whatever the rest of the line says. Failed commands are not remembered. A line that does not parse is `ErrInvalid` whatever key it carries. A replay appends no events.

Done means: `go build ./... && go vet ./... && go test ./...` pass on `main` at `origin`, and each package has tests of its own.
