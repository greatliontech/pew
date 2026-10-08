package run

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/greatliontech/gofresh"
	"golang.org/x/perf/benchfmt"
)

// EncodeFingerprint wraps Gofresh's native JSON in a benchmark-format-safe token.
func EncodeFingerprint(fp gofresh.Fingerprint) (string, error) {
	data, err := json.Marshal(fp)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeFingerprint delegates the entire JSON record grammar to Gofresh.
// Proof integrity is a checking concern, not an additional decoding policy.
func DecodeFingerprint(encoded string) (gofresh.Fingerprint, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != encoded {
		return gofresh.Fingerprint{}, fmt.Errorf("run: invalid fingerprint base64 encoding")
	}
	var fp gofresh.Fingerprint
	if err := json.Unmarshal(data, &fp); err != nil {
		return gofresh.Fingerprint{}, err
	}
	return fp, nil
}

// IsFingerprintProjection reports a reserved former recording key. These names
// remain reserved in benchmark stdout and refuse as file config in format 4.
func IsFingerprintProjection(key string) bool {
	for _, k := range FingerprintProjectionKeys {
		if k.Name == key {
			return true
		}
	}
	return false
}

// RecordedFingerprint admits the payload's measurement shape without interpreting
// observation proof semantics or supplying any missing evidence.
func RecordedFingerprint(cfg []benchfmt.Config) (gofresh.Fingerprint, error) {
	encoded := ""
	seen := map[string]bool{}
	for _, c := range cfg {
		if IsRecordingKey(c.Key) {
			if seen[c.Key] {
				return gofresh.Fingerprint{}, fmt.Errorf("run: duplicate %s", c.Key)
			}
			seen[c.Key] = true
		}
		if IsFingerprintProjection(c.Key) {
			return gofresh.Fingerprint{}, fmt.Errorf("run: parallel fingerprint field %s", c.Key)
		}
		if c.Key == KeyFingerprint.Name {
			encoded = string(c.Value)
		}
	}
	fp, err := DecodeFingerprint(encoded)
	if err != nil {
		return gofresh.Fingerprint{}, err
	}
	if fp.ResultKind != gofresh.Measurement || fp.MaximalClosure == "" || fp.TestVariantClosure == "" || fp.Guards.Toolchain == "" || fp.Guards.BuildConfig == "" || fp.Guards.Machine == "" || fp.Guards.RuntimeConfig == "" || fp.RuntimeInputs == "" || fp.RuntimeDigest == "" {
		return gofresh.Fingerprint{}, fmt.Errorf("run: incomplete measurement fingerprint")
	}
	return fp, nil
}

// FingerprintValue derives a display or comparison value from the native record.
func FingerprintValue(fp gofresh.Fingerprint, key string) string {
	switch key {
	case KeyToolchain.Name:
		return fp.Guards.Toolchain
	case KeyMachine.Name:
		return fp.Guards.Machine
	case KeyBuildConfig.Name:
		return fp.Guards.BuildConfig
	case KeyRuntimeConfig.Name:
		return fp.Guards.RuntimeConfig
	case KeyClosure.Name:
		return fp.MaximalClosure
	case KeyTestVariants.Name:
		return fp.TestVariantClosure
	case KeyClosureStrategy.Name:
		return fp.ClosureStrategy
	case KeyDynamicState.Name:
		return fp.DynamicStateStrategy
	case KeyPurity.Name:
		return fp.PurityAssertion
	case KeyVouches.Name:
		return fp.DynamicStateVouches
	case KeySingleSubjectDischarges.Name:
		return fp.SingleSubjectDischarges
	case KeyPackageProcessDischarges.Name:
		return fp.PackageProcessDischarges
	case KeyRuntime.Name:
		return fp.RuntimeDigest
	case KeyRuntimeInputs.Name:
		return fp.RuntimeInputs
	}
	return ""
}

// ComparisonValue reads persisted facts from the native payload. Non-recorded
// A/B results carry only transient guard projections, never a persisted payload.
func ComparisonValue(r *benchfmt.Result, key string) string {
	return ComparisonValues(r)(key)
}

// ComparisonValues decodes once for one result's derived projections. It does
// not attach mutable projections to the result or permit them to be persisted.
func ComparisonValues(r *benchfmt.Result) func(string) string {
	if _, present := r.ConfigIndex(KeyFingerprint.Name); present {
		fp, err := DecodeFingerprint(r.GetConfig(KeyFingerprint.Name))
		return func(key string) string {
			if IsFingerprintProjection(key) {
				if err != nil {
					return ""
				}
				return FingerprintValue(fp, key)
			}
			return r.GetConfig(key)
		}
	}
	return r.GetConfig
}
