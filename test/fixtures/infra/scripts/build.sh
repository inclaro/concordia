#!/usr/bin/env bash
set -euo pipefail

docker build -t registry.example.com/payments/api:1.2.0 -f docker/Dockerfile .
