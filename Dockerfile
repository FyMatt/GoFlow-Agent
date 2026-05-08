# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/goflow ./cmd/goflow
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/file_tools ./mcp_servers/file_tools
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/skill_runner ./mcp_servers/skill_runner
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/web_tools ./mcp_servers/web_tools

FROM python:3.13-slim AS runtime

WORKDIR /app
RUN mkdir -p /app/bin /app/configs /app/mcp_servers /workspace

COPY --from=builder /out/goflow /usr/local/bin/goflow
COPY --from=builder /out/file_tools /app/bin/file_tools
COPY --from=builder /out/skill_runner /app/bin/skill_runner
COPY --from=builder /out/web_tools /app/bin/web_tools
COPY configs /app/configs
COPY skills /app/skills
COPY mcp_servers/python_notes.py /app/mcp_servers/python_notes.py
COPY docs /app/docs
COPY README.md README.zh-CN.md /app/

EXPOSE 8080
ENTRYPOINT ["goflow"]
CMD ["--config", "/app/configs/goflow.docker.yaml", "--workspace", "/workspace", "--http", ":8080"]
