#!/bin/bash
# Setup hook for the agentic Jira solver (sourced before Claude runs).
# Idempotent; no services are required.

go mod download
