# oss-radar analysis log

Append-only lab log for the scan pipeline. Every run appends a dated stats block
below. Curated findings, gate tuning notes and source dead-ends get edited in by
hand under Findings. Machine-readable twin: runs.jsonl.

## Findings

- 2026-07-08: the stars:>N qualifier is broken on the GitHub issues search API
  (advanced_search backend). Combined with language:go it returns 0 results, and
  alone it returns a tiny wrong set (133 issues globally, wrong repos included).
  Dead end for query-side star filtering. Fix: queries carry only label, language
  and archived; the search adapter enforces min_stars from live repo metadata,
  cached per run (about 1 extra call per distinct repo).
- 2026-07-08: first full watchlist scan (8 repos, 630 issues): 14 eligible,
  47 quarantine, 569 rejected, 257 api calls. The search source adds far less
  signal per call: 62 star-floor survivors, 0 eligible, 16 quarantine, 128 calls.
  Watchlist depth beats search breadth, as the strategy note predicted.
- 2026-07-08: miekg/dns currently has zero open issues that are not PRs, so it
  contributes nothing to the pool. Expected for a tightly run tracker; keep it
  watched, the cursor makes rescans nearly free.

---

## 2026-07-08 19:18 UTC | source=watchlist
scanned=630 new=630 updated=0 | eligible=14 quarantine=47 rejected=569 | api_calls=257 duration_ms=151243
- coredns/coredns: scanned=142 new=142 eligible=3 quarantine=14 rejected=125
- jaegertracing/jaeger: scanned=70 new=70 eligible=2 quarantine=1 rejected=67
- open-telemetry/opentelemetry-collector: scanned=135 new=135 eligible=4 quarantine=10 rejected=121
- open-telemetry/opentelemetry-collector-contrib: scanned=69 new=69 eligible=4 quarantine=0 rejected=65
- open-telemetry/opentelemetry-go: scanned=125 new=125 eligible=1 quarantine=13 rejected=111
- prometheus/prometheus: scanned=54 new=54 eligible=0 quarantine=6 rejected=48
- unikraft/kraftkit: scanned=35 new=35 eligible=0 quarantine=3 rejected=32

## 2026-07-08 19:21 UTC | source=watchlist
scanned=1 new=0 updated=1 | eligible=0 quarantine=0 rejected=1 | api_calls=16 duration_ms=7574
- open-telemetry/opentelemetry-collector-contrib: scanned=1 new=0 eligible=0 quarantine=0 rejected=1

## 2026-07-08 19:22 UTC | source=watchlist
scanned=630 new=0 updated=630 | eligible=14 quarantine=47 rejected=569 | api_calls=257 duration_ms=154875
- coredns/coredns: scanned=142 new=0 eligible=3 quarantine=14 rejected=125
- jaegertracing/jaeger: scanned=70 new=0 eligible=2 quarantine=1 rejected=67
- open-telemetry/opentelemetry-collector: scanned=135 new=0 eligible=4 quarantine=10 rejected=121
- open-telemetry/opentelemetry-collector-contrib: scanned=69 new=0 eligible=4 quarantine=0 rejected=65
- open-telemetry/opentelemetry-go: scanned=125 new=0 eligible=1 quarantine=13 rejected=111
- prometheus/prometheus: scanned=54 new=0 eligible=0 quarantine=6 rejected=48
- unikraft/kraftkit: scanned=35 new=0 eligible=0 quarantine=3 rejected=32

## 2026-07-08 19:25 UTC | source=search
scanned=0 new=0 updated=0 | eligible=0 quarantine=0 rejected=0 | api_calls=2 duration_ms=1235

## 2026-07-08 19:27 UTC | source=search
scanned=62 new=51 updated=11 | eligible=0 quarantine=16 rejected=46 | api_calls=128 duration_ms=80197
- QuantumNous/new-api: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- VictoriaMetrics/VictoriaMetrics: scanned=2 new=2 eligible=0 quarantine=0 rejected=2
- agones-dev/agones: scanned=2 new=1 eligible=0 quarantine=0 rejected=2
- alibaba/open-code-review: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- aquasecurity/trivy: scanned=2 new=1 eligible=0 quarantine=2 rejected=0
- ccfos/huatuo: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- docker/model-runner: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- dolthub/dolt: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- goharbor/harbor: scanned=2 new=2 eligible=0 quarantine=0 rejected=2
- golang/go: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- grpc-ecosystem/grpc-gateway: scanned=2 new=1 eligible=0 quarantine=0 rejected=2
- hashicorp/terraform-provider-aws: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- helm/helm: scanned=2 new=1 eligible=0 quarantine=0 rejected=2
- higress-group/higress: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- hkdb/aerion: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- ipfs-cluster/ipfs-cluster: scanned=2 new=1 eligible=0 quarantine=2 rejected=0
- jaegertracing/jaeger: scanned=1 new=0 eligible=0 quarantine=0 rejected=1
- kai-scheduler/KAI-Scheduler: scanned=3 new=3 eligible=0 quarantine=1 rejected=2
- kubeflow/trainer: scanned=2 new=1 eligible=0 quarantine=0 rejected=2
- kubernetes-sigs/kustomize: scanned=2 new=1 eligible=0 quarantine=0 rejected=2
- kubernetes/kubernetes: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- kubestellar/kubestellar: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- kumahq/kuma: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- kyverno/kyverno: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- moby/moby: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- netdata/netdata: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- nginx/nginx-gateway-fabric: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- oauth2-proxy/oauth2-proxy: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- openbao/openbao: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- openfga/openfga: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- padok-team/burrito: scanned=4 new=4 eligible=0 quarantine=1 rejected=3
- panjf2000/gnet: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- perber/leafwiki: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
- pipe-cd/pipecd: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- prometheus/prometheus: scanned=3 new=0 eligible=0 quarantine=0 rejected=3
- rclone/rclone: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- sustainable-computing-io/kepler: scanned=3 new=3 eligible=0 quarantine=0 rejected=3
- tektoncd/pipeline: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- vllm-project/semantic-router: scanned=3 new=3 eligible=0 quarantine=0 rejected=3
- volcano-sh/volcano: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- zalando/skipper: scanned=1 new=1 eligible=0 quarantine=0 rejected=1
- zzet/gortex: scanned=1 new=1 eligible=0 quarantine=1 rejected=0
