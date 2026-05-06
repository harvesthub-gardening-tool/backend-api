#!/usr/bin/env bash
set -euo pipefail

go test ./internal/service -run 'TestControlService_Task20HarnessLifecycleAndFailureMatrix' -count=1 -v
