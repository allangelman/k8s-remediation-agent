# evals

The eval harness runs each fault scenario several times: inject the fault, run
the agent, check whether the app recovered, and record the result. It measures:

- Correct diagnosis rate
- Correct remediation rate
- Time to recovery
- Unsafe or denied actions (target: 0)
