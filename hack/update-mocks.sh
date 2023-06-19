#!/usr/bin/env bash

#https://github.com/golang/mock#gomock
mockgen -build_flags=--mod=mod -destination=pkg/jobrunaggregator/jobrunaggregatorlib/ci_data_client_mock.go -package=jobrunaggregatorlib github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib CIDataClient
