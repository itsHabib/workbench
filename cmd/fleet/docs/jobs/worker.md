# Job worker

For a watcher `jobs` binding, the bridge claims, renews and reports automatically.
Implement the supplied brief, preserve work, and return the artifact, revision, checks
and limitations. Do not issue a second claim or complete/accept it yourself.

The manual CLI/HTTP workflow below applies when no automatic binding is configured.

On a wake, inspect your current job before claiming more. Use a stable worker identity
and save each claim retry key before issuing it; replay that key after an uncertain response.
A claim returns the job brief and attempt token. Follow the brief in your isolated workspace.
Renew the lease before expiry, including during long commands. If you cannot keep renewing,
stop starting effects and report the situation to the coordinator; do not assume you own work.

Return an artifact reference, exact revision, checks and limitations with `fleet job complete`.
Completion reports work; only the coordinator accepts it. A stale-token rejection means
preserve the artifact and notify the coordinator rather than forcing it into another attempt.
When no claimable work remains, leave a concise Fleet handoff and finish the turn. The
existing watcher owns future wakes. Do not spawn a polling daemon or start other workers.
