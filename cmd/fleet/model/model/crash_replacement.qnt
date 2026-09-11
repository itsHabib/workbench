// One branch/resource, one original session with one child, one replacement.
// Session death and child termination are independent. No PID reuse, restart,
// revoke, arbitrary shell parsing, or filesystem failure is modeled.
module CrashReplacement {
  const resource: bool
  // Hypothetical oracle: all old effects have stopped, not merely the parent.
  // Fleet does not implement this guard for branches.
  const requireQuiescence: bool

  var owner: str
  var parentAlive: bool
  var childAlive: bool
  var childStarted: bool
  var staleWrite: bool
  var replacementWrote: bool
  var lastAction: str

  action state(o: str, p: bool, c: bool, started: bool, stale: bool, wrote: bool, a: str): bool = all {
    owner' = o, parentAlive' = p, childAlive' = c,
    childStarted' = started, staleWrite' = stale,
    replacementWrote' = wrote, lastAction' = a,
  }

  action init = all {
    owner' = "none", parentAlive' = true, childAlive' = false,
    childStarted' = false, staleWrite' = false,
    replacementWrote' = false, lastAction' = "init",
  }

  action claim = all {
    owner == "none", parentAlive,
    state("A", parentAlive, childAlive, childStarted, staleWrite, replacementWrote, "claim"),
  }
  action spawnChild = all {
    owner == "A", parentAlive, not(childStarted),
    state(owner, parentAlive, true, true, staleWrite, replacementWrote, "spawnChild"),
  }
  action crashParent = all {
    owner == "A", parentAlive,
    state(owner, false, childAlive, childStarted, staleWrite, replacementWrote, "crashParent"),
  }
  action replace = all {
    owner == "A", not(parentAlive),
    val allowed = not(resource) and (not(requireQuiescence) or not(childAlive))
    state(if (allowed) "B" else owner, parentAlive, childAlive, childStarted,
      staleWrite, replacementWrote, if (allowed) "replace" else "refuseReplacement"),
  }
  action childWrite = all {
    childAlive,
    state(owner, parentAlive, childAlive, childStarted, staleWrite or owner != "A",
      replacementWrote, "childWrite"),
  }
  action replacementWrite = all {
    owner == "B",
    state(owner, parentAlive, childAlive, childStarted, staleWrite, true, "replacementWrite"),
  }
  action childExit = all {
    childAlive,
    state(owner, parentAlive, false, childStarted, staleWrite, replacementWrote, "childExit"),
  }
  action idle = state(owner, parentAlive, childAlive, childStarted, staleWrite, replacementWrote, "idle")
  action step = any { claim, spawnChild, crashParent, replace, childWrite, replacementWrite, childExit, idle }

  // Re-export the discovered witness through the SAME guarded transitions in a
  // fixed order. Safety checks above use unrestricted step; the frozen fixture
  // must not depend on the solver choosing among equivalent unsafe schedules.
  action witnessStep = any {
    all { lastAction == "init", claim },
    all { lastAction == "claim", spawnChild },
    all { lastAction == "spawnChild", crashParent },
    all { lastAction == "crashParent", replace },
    all { lastAction == "replace", replacementWrite },
    all { lastAction == "replacementWrite", childWrite },
  }

  val noStaleChildWrite = not(staleWrite)
  // Stronger witness: the replacement has also performed a write.
  val noConflictingEffects = not(staleWrite and replacementWrote)
  val resourceRetained = resource implies owner != "B"
}

module CrashBranch {
  import CrashReplacement(resource = false, requireQuiescence = false).*
}
module CrashResource {
  import CrashReplacement(resource = true, requireQuiescence = false).*
}
module QuiescentBranch {
  import CrashReplacement(resource = false, requireQuiescence = true).*
}
