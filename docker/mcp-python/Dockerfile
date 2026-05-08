# syntax=docker/dockerfile:1

FROM python:3.13-slim

LABEL org.opencontainers.image.title="GoFlow MCP Python Tool Runtime"
LABEL org.opencontainers.image.description="Minimal Python runtime for GoFlow containerized MCP tool scaffolds"

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /workspace
RUN mkdir -p /workspace /goflow-tools

CMD ["python", "--version"]
