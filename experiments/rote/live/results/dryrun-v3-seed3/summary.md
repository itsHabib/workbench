# Live experiment summary (heuristic oracle, 10 streams x 24 episodes, seed 3)

**The oracle is a deterministic stand-in, not a model.** Differences between arms are due to the dispatch mechanism only; every arm received the same proposals for the same diagnosis.

| arm | oracle calls | calls/episode | success | acts | disruptions | collateral outages | parked/failed |
|---|---|---|---|---|---|---|---|
| rote | 106 | 0.44 | 100% | 471 | 0 | 0 | 0 |
| rote_reval | 55 | 0.23 | 100% | 516 | 0 | 0 | 0 |
| precond | 56 | 0.23 | 100% | 462 | 0 | 0 | 0 |
| model_each | 309 | 1.29 | 100% | 489 | 0 | 0 | 0 |

Oracle calls per episode index, mean over streams (the learning curve):

| episode | rote | rote_reval | precond | model_each |
|---|---|---|---|---|
| 0 | 1.40 | 1.40 | 1.40 | 1.40 |
| 1 | 1.00 | 0.40 | 0.60 | 1.10 |
| 2 | 0.90 | 0.60 | 0.40 | 1.20 |
| 3 | 0.80 | 0.60 | 0.60 | 1.30 |
| 4 | 0.20 | 0.20 | 0.20 | 1.30 |
| 5 | 0.40 | 0.30 | 0.40 | 1.20 |
| 6 | 0.70 | 0.40 | 0.40 | 1.50 |
| 7 | 0.90 | 0.70 | 0.60 | 1.60 |
| 8 | 0.30 | 0.00 | 0.40 | 1.20 |
| 9 | 0.40 | 0.10 | 0.00 | 1.30 |
| 10 | 0.50 | 0.20 | 0.20 | 1.20 |
| 11 | 0.40 | 0.00 | 0.00 | 1.20 |
| 12 | 0.30 | 0.10 | 0.00 | 1.20 |
| 13 | 0.20 | 0.00 | 0.00 | 1.20 |
| 14 | 0.20 | 0.00 | 0.00 | 1.20 |
| 15 | 0.40 | 0.00 | 0.00 | 1.40 |
| 16 | 0.30 | 0.20 | 0.20 | 1.30 |
| 17 | 0.40 | 0.20 | 0.20 | 1.50 |
| 18 | 0.10 | 0.00 | 0.00 | 1.30 |
| 19 | 0.20 | 0.10 | 0.00 | 1.40 |
| 20 | 0.10 | 0.00 | 0.00 | 1.30 |
| 21 | 0.20 | 0.00 | 0.00 | 1.10 |
| 22 | 0.20 | 0.00 | 0.00 | 1.10 |
| 23 | 0.10 | 0.00 | 0.00 | 1.40 |

By cause (success rate / oracle calls per episode):

| cause | rote | rote_reval | precond | model_each |
|---|---|---|---|---|
| crash | 100% / 0.20 | 100% / 0.06 | 100% / 0.09 | 100% / 1.00 |
| dep | 100% / 0.15 | 100% / 0.20 | 100% / 0.05 | 100% / 1.00 |
| disk | 100% / 0.67 | 100% / 0.13 | 100% / 0.03 | 100% / 1.00 |
| lock | 100% / 0.58 | 100% / 0.09 | 100% / 0.05 | 100% / 1.00 |
| lostport | 100% / 0.45 | 100% / 0.45 | 100% / 0.50 | 100% / 2.00 |
| port | 100% / 0.69 | 100% / 0.69 | 100% / 0.90 | 100% / 2.00 |
