# Live experiment summary (heuristic oracle, 10 streams x 24 episodes, seed 2)

**The oracle is a deterministic stand-in, not a model.** Differences between arms are due to the dispatch mechanism only; every arm received the same proposals for the same diagnosis.

| arm | oracle calls | calls/episode | success | acts | disruptions | collateral outages | parked/failed |
|---|---|---|---|---|---|---|---|
| rote | 117 | 0.49 | 100% | 478 | 0 | 0 | 0 |
| rote_reval | 57 | 0.24 | 100% | 511 | 0 | 0 | 0 |
| precond | 64 | 0.27 | 100% | 472 | 0 | 0 | 0 |
| model_each | 321 | 1.34 | 100% | 503 | 0 | 0 | 0 |

Oracle calls per episode index, mean over streams (the learning curve):

| episode | rote | rote_reval | precond | model_each |
|---|---|---|---|---|
| 0 | 1.30 | 1.30 | 1.30 | 1.30 |
| 1 | 1.10 | 0.80 | 0.90 | 1.50 |
| 2 | 1.30 | 1.00 | 1.00 | 1.40 |
| 3 | 0.50 | 0.00 | 0.20 | 1.40 |
| 4 | 1.00 | 0.80 | 0.80 | 1.50 |
| 5 | 0.60 | 0.30 | 0.20 | 1.20 |
| 6 | 0.60 | 0.30 | 0.20 | 1.20 |
| 7 | 0.50 | 0.30 | 0.40 | 1.40 |
| 8 | 0.20 | 0.00 | 0.00 | 1.20 |
| 9 | 0.20 | 0.00 | 0.00 | 1.10 |
| 10 | 0.50 | 0.00 | 0.00 | 1.10 |
| 11 | 0.60 | 0.20 | 0.20 | 1.10 |
| 12 | 0.40 | 0.20 | 0.40 | 1.60 |
| 13 | 0.60 | 0.20 | 0.20 | 1.20 |
| 14 | 0.20 | 0.00 | 0.00 | 1.50 |
| 15 | 0.40 | 0.00 | 0.00 | 1.30 |
| 16 | 0.10 | 0.00 | 0.00 | 1.20 |
| 17 | 0.00 | 0.00 | 0.00 | 1.40 |
| 18 | 0.30 | 0.10 | 0.20 | 1.30 |
| 19 | 0.40 | 0.20 | 0.20 | 1.50 |
| 20 | 0.20 | 0.00 | 0.20 | 1.40 |
| 21 | 0.50 | 0.00 | 0.00 | 1.30 |
| 22 | 0.10 | 0.00 | 0.00 | 1.50 |
| 23 | 0.10 | 0.00 | 0.00 | 1.50 |

By cause (success rate / oracle calls per episode):

| cause | rote | rote_reval | precond | model_each |
|---|---|---|---|---|
| crash | 100% / 0.28 | 100% / 0.06 | 100% / 0.09 | 100% / 1.00 |
| dep | 100% / 0.27 | 100% / 0.27 | 100% / 0.00 | 100% / 1.00 |
| disk | 100% / 0.71 | 100% / 0.06 | 100% / 0.03 | 100% / 1.00 |
| lock | 100% / 0.66 | 100% / 0.17 | 100% / 0.06 | 100% / 1.00 |
| lostport | 100% / 0.45 | 100% / 0.45 | 100% / 0.50 | 100% / 2.00 |
| port | 100% / 0.54 | 100% / 0.54 | 100% / 0.86 | 100% / 2.00 |
