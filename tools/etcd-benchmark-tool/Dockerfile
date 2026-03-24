FROM golang:1.23 AS builder
ARG ETCD_VER=v3.5.21
RUN git clone --depth 1 --branch ${ETCD_VER} https://github.com/etcd-io/etcd.git /tmp/etcd \
    && cd /tmp/etcd/tools/benchmark && go build -o /usr/local/bin/benchmark .
RUN curl -sL "https://github.com/etcd-io/etcd/releases/download/${ETCD_VER}/etcd-${ETCD_VER}-linux-amd64.tar.gz" \
    | tar xz --strip-components=1 -C /usr/local/bin "etcd-${ETCD_VER}-linux-amd64/etcdctl"

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
RUN microdnf install -y coreutils-single && microdnf clean all
COPY --from=builder /usr/local/bin/benchmark /usr/local/bin/etcdctl /usr/local/bin/
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENV WORKERS=1 RANGE_WORKERS=0 CLIENTS=50 KEY_SIZE=256 VAL_SIZE=10240 KEY_SPACE_SIZE=380000 PUT_TOTAL=380000 RANGE_TOTAL=380000
ENV MODE=benchmark

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
