package main

import "testing"

func TestLoadRuntimeConfigKDFAdmissionDefaultsAndOverrides(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL":             "postgres://example",
		"GINBAR_MEDIA_SOURCE_ROOT": "/srv/ginbar/media",
	}
	cfg, err := loadRuntimeConfig(mapEnv(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.api.Auth.KDFAdmission.MaxConcurrent != 1 || cfg.api.Auth.KDFAdmission.MaxQueued != 4 {
		t.Fatalf("default KDF admission=%#v", cfg.api.Auth.KDFAdmission)
	}

	env := cloneEnv(base)
	env["GINBAR_AUTH_KDF_MAX_CONCURRENT"] = "2"
	env["GINBAR_AUTH_KDF_MAX_QUEUED"] = "0"
	cfg, err = loadRuntimeConfig(mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.api.Auth.KDFAdmission.MaxConcurrent != 2 || cfg.api.Auth.KDFAdmission.MaxQueued != 0 {
		t.Fatalf("override KDF admission=%#v", cfg.api.Auth.KDFAdmission)
	}
}

func TestLoadRuntimeConfigRejectsInvalidKDFAdmissionLimits(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL":             "postgres://example",
		"GINBAR_MEDIA_SOURCE_ROOT": "/srv/ginbar/media",
	}
	for _, tt := range []struct {
		key   string
		value string
	}{
		{key: "GINBAR_AUTH_KDF_MAX_CONCURRENT", value: "0"},
		{key: "GINBAR_AUTH_KDF_MAX_CONCURRENT", value: "nope"},
		{key: "GINBAR_AUTH_KDF_MAX_QUEUED", value: "-1"},
		{key: "GINBAR_AUTH_KDF_MAX_QUEUED", value: "nope"},
	} {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			env := cloneEnv(base)
			env[tt.key] = tt.value
			if _, err := loadRuntimeConfig(mapEnv(env)); err == nil {
				t.Fatalf("%s=%q unexpectedly accepted", tt.key, tt.value)
			}
		})
	}
}
