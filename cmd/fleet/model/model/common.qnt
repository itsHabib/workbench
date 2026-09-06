module Common {
  // Two sessions compete for one key. The key's kind decides what a dead holder
  // means: a branch is taken over silently, a resource is orphaned and needs an
  // explicit takeover (a dead holder's machine may still be running).
  type Session = A | B
  type Kind = Branch | Resource

  // What the store can say about a session when a rival reads its record.
  // Unreadable is the case the review found: the record exists but cannot be
  // read (permissions, a half-written file, a full disk). It is evidence that
  // is unavailable, not evidence of death.
  type Live = Alive | Dead | Unreadable

  type Holder = Nobody | Held(Session)

  // Facts are the audit: what was authorized, and how.
  type Fact =
    | AuthorizedA
    | AuthorizedB
    | TookOverA
    | TookOverB
    | RefusedA
    | RefusedB
    // A resource changing hands after a death with no person confirming it.
    | SilentA
    | SilentB

  pure def other(s: Session): Session = match s { A => B | B => A }
  pure def authFact(s: Session): Fact = match s { A => AuthorizedA | B => AuthorizedB }
  pure def tookFact(s: Session): Fact = match s { A => TookOverA | B => TookOverB }
  pure def refusedFact(s: Session): Fact = match s { A => RefusedA | B => RefusedB }
  pure def silentFact(s: Session): Fact = match s { A => SilentA | B => SilentB }
}
