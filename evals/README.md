# evals

The eval harness runs each fault scenario several times: inject the fault, run
the agent, check whether the app recovered, and record the result. It measures:

- Correct diagnosis rate
- Correct action rate: the expected action (`revert_commit`, `sync_app`, or
  none, i.e. escalate) taken and, if any, recovery verified
- Time to recovery
- Unsafe or denied actions (target: 0)

Scenarios and their expected actions are defined at the top of `main.go`.
Definitions are in the main README's [Evals](../README.md#evals) section.
