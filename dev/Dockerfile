# Dockerfile to build a fake-statsd client that outputs to stdout
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    curl \
    netcat-traditional \
    socat \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --user-group fake
USER fake
