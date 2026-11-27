package main

import (
	"fmt"
	"strconv"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	fsAnnotation     = "sysrunc.io/fs"
	tickerAnnotation = "sysrunc.io/ticker"
)

type runtimeFeatures struct {
	fs     bool
	ticker bool
}

func featuresForSpec(spec *specs.Spec, _ string) (runtimeFeatures, error) {
	features := runtimeFeatures{}
	for _, entry := range []struct {
		key   string
		value *bool
	}{{fsAnnotation, &features.fs}, {tickerAnnotation, &features.ticker}} {
		if raw, ok := spec.Annotations[entry.key]; ok {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return features, fmt.Errorf("invalid %s annotation %q: %w", entry.key, raw, err)
			}
			*entry.value = value
		}
	}
	if features.ticker && !features.fs {
		return features, fmt.Errorf("%s requires %s=true", tickerAnnotation, fsAnnotation)
	}
	return features, nil
}
