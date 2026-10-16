# faults

Scripts that break the sample app in specific, repeatable ways:

1. Bad image tag → `ImagePullBackOff`
2. Bad deploy → `CrashLoopBackOff`
3. Memory limit too low → `OOMKilled`
4. Failing readiness probe
5. ArgoCD app drifting `OutOfSync`
