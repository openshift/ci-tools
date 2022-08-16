#!/usr/bin/env bash

#https://github.com/golang/mock#gomock
mockgen -build_flags=--mod=mod -destination=pkg/jobrunaggregator/jobruntestcaseanalyzer/cidataclient_test.go -package=jobruntestcaseanalyzer github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib CIDataClient