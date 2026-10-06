# Experiments

Bounded prototypes and their evidence, separate from supported Workbench tools.

- [Terraform + Fleet + Rooms](terraform-fleet/cloud/RESULTS.md): real local
  Fleet agents and exact-patch verification in a GCP Room. Includes the actual
  Terraform configuration, passing audit, failed attempts and teardown evidence.
- [Language bakeoff archive](https://github.com/itsHabib/bakeoffs/tree/main/workbench-language-09-14):
  the HCL, custom-language and typed-Go POCs, their baselines, and the decision
  to keep plan/evidence semantics while avoiding a Workbench-owned language.
- [Rote](rote/README.md): a tiny effect-typed language whose evaluator infers the
  world-assumptions of every validated agent-built script, so retained capabilities
  replay without a model and refuse to run where they were never validated. Design,
  runnable prototype, a nine-episode workload against a plain-Python baseline, and the
  cases it catches and the ones it cannot.
