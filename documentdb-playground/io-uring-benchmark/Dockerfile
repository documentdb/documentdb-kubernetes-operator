# Optional pre-baked image for the benchmark runner, so you don't clone + pip install
# at pod startup on every run.
#
#   docker build -t <registry>/iouring-bench-runner:latest \
#     --build-arg MICRO_BENCH_REF=main .
#   docker push <registry>/iouring-bench-runner:latest
#
# Then set `image:` in manifests/benchmark-runner.yaml to your pushed image and replace
# the bootstrap `args:` with: ["-c", "touch /tmp/ready && sleep infinity"].
FROM python:3.12-slim

ARG MICRO_BENCH_REPO=https://github.com/documentdb/micro-benchmarks.git
ARG MICRO_BENCH_REF=main

RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates build-essential procps \
 && rm -rf /var/lib/apt/lists/*

RUN git clone "${MICRO_BENCH_REPO}" /opt/micro-benchmarks \
 && cd /opt/micro-benchmarks \
 && git checkout "${MICRO_BENCH_REF}" \
 && pip install --no-cache-dir -r requirements.txt

WORKDIR /opt/micro-benchmarks
RUN mkdir -p /results
CMD ["/bin/bash", "-c", "touch /tmp/ready && sleep infinity"]
