FROM golang:1.21-bullseye as builder

ARG CONSUL_URL="https://releases.hashicorp.com/consul/1.15.1/consul_1.15.1_linux_amd64.zip"
ARG CONSUL_SHA="23f7eb0461dd01a95c5d56472b91c22d5dacec84f31f1846c0c9f9621f98f29f"
RUN apt-get update && \
      apt-get install -y \
      git \
      unzip
RUN curl -s "$CONSUL_URL" -o /tmp/consul.zip && \
      echo "$CONSUL_SHA /tmp/consul.zip" | sha256sum -c && \
      unzip /tmp/consul.zip -d /usr/local/bin

WORKDIR /src/kube-service-exporter
COPY . .
COPY .git .git
RUN make

FROM debian:buster-slim
COPY --from=builder /src/kube-service-exporter/bin/kube-service-exporter /usr/local/bin
RUN useradd --create-home --user-group nonroot
USER nonroot
ENTRYPOINT ["/usr/local/bin/kube-service-exporter"]
