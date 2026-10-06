# Live experiment summary (heuristic oracle, 10 streams x 24 episodes, seed 1)

**The oracle is a deterministic stand-in, not a model.** Differences between arms are due to the dispatch mechanism only; every arm received the same proposals for the same diagnosis.

| arm | oracle calls | calls/episode | success | acts | disruptions | collateral outages | parked/failed |
|---|---|---|---|---|---|---|---|
| rote | 136 | 0.57 | 100% | 491 | 0 | 0 | 0 |
| rote_reval | 84 | 0.35 | 100% | 544 | 0 | 0 | 0 |
| precond | 58 | 0.24 | 100% | 465 | 0 | 0 | 0 |
| model_each | 315 | 1.31 | 100% | 497 | 0 | 0 | 0 |

Oracle calls per episode index, mean over streams (the learning curve):

| episode | rote | rote_reval | precond | model_each |
|---|---|---|---|---|
| 0 | 1.40 | 1.40 | 1.40 | 1.40 |
| 1 | 1.40 | 1.00 | 1.10 | 1.40 |
| 2 | 0.80 | 0.70 | 0.80 | 1.50 |
| 3 | 0.30 | 0.00 | 0.00 | 1.20 |
| 4 | 0.40 | 0.20 | 0.10 | 1.20 |
| 5 | 0.80 | 0.20 | 0.60 | 1.30 |
| 6 | 0.50 | 0.30 | 0.20 | 1.40 |
| 7 | 0.50 | 0.40 | 0.40 | 1.30 |
| 8 | 0.50 | 0.00 | 0.00 | 1.00 |
| 9 | 0.80 | 0.40 | 0.20 | 1.30 |
| 10 | 0.80 | 0.80 | 0.60 | 1.60 |
| 11 | 0.70 | 0.20 | 0.00 | 1.10 |
| 12 | 0.80 | 0.60 | 0.00 | 1.50 |
| 13 | 0.50 | 0.30 | 0.00 | 1.50 |
| 14 | 0.40 | 0.10 | 0.00 | 1.30 |
| 15 | 0.40 | 0.30 | 0.20 | 1.30 |
| 16 | 0.20 | 0.10 | 0.00 | 1.10 |
| 17 | 0.20 | 0.20 | 0.00 | 1.30 |
| 18 | 0.50 | 0.20 | 0.00 | 1.20 |
| 19 | 0.30 | 0.40 | 0.00 | 1.50 |
| 20 | 0.20 | 0.00 | 0.00 | 1.10 |
| 21 | 0.30 | 0.20 | 0.00 | 1.10 |
| 22 | 0.40 | 0.20 | 0.00 | 1.30 |
| 23 | 0.50 | 0.20 | 0.20 | 1.60 |

By cause (success rate / oracle calls per episode):

| cause | rote | rote_reval | precond | model_each |
|---|---|---|---|---|
| crash | 100% / 0.21 | 100% / 0.03 | 100% / 0.05 | 100% / 1.00 |
| dep | 100% / 0.24 | 100% / 0.24 | 100% / 0.06 | 100% / 1.00 |
| disk | 100% / 0.57 | 100% / 0.16 | 100% / 0.10 | 100% / 1.00 |
| lock | 100% / 0.69 | 100% / 0.22 | 100% / 0.03 | 100% / 1.00 |
| lostport | 100% / 0.98 | 100% / 0.82 | 100% / 0.53 | 100% / 2.00 |
| port | 100% / 0.69 | 100% / 0.85 | 100% / 0.85 | 100% / 2.00 |
