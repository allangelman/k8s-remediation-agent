# faults

Scripts that break the sample app in specific, repeatable ways. Faults that
simulate a bad deploy push a commit to the `demo-env` branch (the one ArgoCD
deploys from), so there's real deploy history for the agent to investigate.
`make reset` undoes any fault.

1. Bad image tag → `ImagePullBackOff` (`make break-image`, `bad-image-tag.sh`)
2. Bad deploy → `CrashLoopBackOff`
3. Memory limit too low → `OOMKilled`
4. Failing readiness probe
5. ArgoCD app drifting `OutOfSync`
