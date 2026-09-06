module Reference {
  import Common.* from "./common"

  const kind: Kind

  var holder: Holder
  var liveA: Live
  var liveB: Live
  // A write the hook authorized and the tool is still executing.
  var inFlightA: bool
  var inFlightB: bool
  var facts: Set[Fact]

  def live(s: Session): Live = match s { A => liveA | B => liveB }
  def inFlight(s: Session): bool = match s { A => inFlightA | B => inFlightB }

  action assignState(h: Holder, la: Live, lb: Live, fa: bool, fb: bool, fs: Set[Fact]): bool = all {
    holder' = h,
    liveA' = la,
    liveB' = lb,
    inFlightA' = fa,
    inFlightB' = fb,
    facts' = fs,
  }

  action init = all {
    holder' = Nobody,
    liveA' = Alive,
    liveB' = Alive,
    inFlightA' = false,
    inFlightB' = false,
    facts' = Set(),
  }

  def setLive(s: Session, l: Live): (Live, Live) = match s {
    | A => (l, liveB)
    | B => (liveA, l)
  }
  def setFlight(s: Session, f: bool): (bool, bool) = match s {
    | A => (f, inFlightB)
    | B => (inFlightA, f)
  }

  // CheckLease, decided atomically under the key's lock, for a live session s.
  // Free: take it. Mine: proceed. Held by a rival: the rival's liveness decides,
  // and only readable evidence of death decides anything.
  action write(s: Session): bool = all {
    live(s) == Alive,
    not(inFlight(s)),
    match holder {
      | Nobody => all {
          val fl = setFlight(s, true)
          assignState(Held(s), liveA, liveB, fl._1, fl._2, facts.union(Set(authFact(s))))
        }
      | Held(h) =>
          if (h == s) all {
            val fl = setFlight(s, true)
            assignState(holder, liveA, liveB, fl._1, fl._2, facts.union(Set(authFact(s))))
          } else match live(h) {
            | Alive => assignState(holder, liveA, liveB, inFlightA, inFlightB, facts.union(Set(refusedFact(s))))
            | Unreadable => assignState(holder, liveA, liveB, inFlightA, inFlightB, facts.union(Set(refusedFact(s))))
            | Dead =>
                if (kind == Resource)
                  assignState(holder, liveA, liveB, inFlightA, inFlightB, facts.union(Set(refusedFact(s))))
                else all {
                  val fl = setFlight(s, true)
                  assignState(Held(s), liveA, liveB, fl._1, fl._2, facts.union(Set(authFact(s), tookFact(s))))
                }
          }
    }
  }

  // The tool call finished; the lease stays until the session ends.
  action finish(s: Session): bool = all {
    inFlight(s),
    val fl = setFlight(s, false)
    assignState(holder, liveA, liveB, fl._1, fl._2, facts),
  }

  // SessionEnd releases what the session held.
  action release(s: Session): bool = all {
    holder == Held(s),
    live(s) == Alive,
    not(inFlight(s)),
    assignState(Nobody, liveA, liveB, inFlightA, inFlightB, facts),
  }

  // A crash: the process is gone, so nothing of its is in flight, and the lease
  // file stays exactly as it was.
  action crash(s: Session): bool = all {
    live(s) == Alive,
    val lv = setLive(s, Dead)
    val fl = setFlight(s, false)
    assignState(holder, lv._1, lv._2, fl._1, fl._2, facts),
  }

  // The session record becomes unreadable to a rival, and readable again. The
  // session itself is alive throughout; it may even be mid-write.
  action obscure(s: Session): bool = all {
    live(s) == Alive,
    val lv = setLive(s, Unreadable)
    assignState(holder, lv._1, lv._2, inFlightA, inFlightB, facts),
  }
  action reveal(s: Session): bool = all {
    live(s) == Unreadable,
    val lv = setLive(s, Alive)
    assignState(holder, lv._1, lv._2, inFlightA, inFlightB, facts),
  }

  // `fleet take --takeover`: a person confirmed a dead holder's resource is quiet.
  action takeover(s: Session): bool = all {
    kind == Resource,
    live(s) == Alive,
    match holder {
      | Held(h) => h != s and live(h) == Dead
      | Nobody => false
    },
    assignState(Held(s), liveA, liveB, inFlightA, inFlightB, facts.union(Set(tookFact(s)))),
  }

  action idle = assignState(holder, liveA, liveB, inFlightA, inFlightB, facts)

  action step = any {
    write(A), write(B),
    finish(A), finish(B),
    release(A), release(B),
    crash(A), crash(B),
    obscure(A), obscure(B),
    reveal(A), reveal(B),
    takeover(A), takeover(B),
    idle,
  }

  // ---- what must hold in every reachable state ----

  // One holder per key, and only the holder writes: a write in flight belongs to
  // the session that holds the key.
  val exclusion = not(inFlightA and inFlightB)
  val writerHolds =
    (inFlightA implies holder == Held(A)) and
    (inFlightB implies holder == Held(B))

  // Unavailable evidence is not death: while the holder is alive (readable or
  // not), the key never changes hands and no rival is authorized.
  val evidenceNotDeath =
    match holder {
      | Held(h) => (live(h) != Dead) implies not(inFlight(other(h)))
      | Nobody => true
    }

  // A dead holder's resource is never taken over silently: the reference never
  // records a silent change of hands; a mutant that does trips this.
  val noSilentResourceTakeover =
    (kind == Resource) implies not(SilentA.in(facts) or SilentB.in(facts))

  val allSafe = exclusion and writerHolds and evidenceNotDeath and noSilentResourceTakeover
}

module ReferenceBranch {
  import Common.* from "./common"
  import Reference(kind = Branch).*
}

module ReferenceResource {
  import Common.* from "./common"
  import Reference(kind = Resource).*
}
