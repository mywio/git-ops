package main

import "testing"

func FuzzParseComposePSOutput(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("\n"),
		[]byte(`{"Name":"api","State":"running"}` + "\n"),
		[]byte(`{"Name":"api","State":"running"}` + "\n" + `{"Name":"db","State":"exited"}` + "\n"),
		[]byte(`not-json` + "\n" + `{"Name":"api","State":"running"}` + "\n"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		containers := parseComposePSOutput(input)
		for _, container := range containers {
			if container.Name != "" && container.Name != string([]byte(container.Name)) {
				t.Fatalf("unexpected invalid name content")
			}
		}
	})
}

func FuzzScanComposeEnvPersistenceRisks(f *testing.F) {
	f.Add("services:\n  api:\n    image: ghcr.io/acme/api:${TOKEN}\n", "TOKEN=secret")
	f.Add("services:\n  api:\n    environment:\n      TOKEN: ${TOKEN}\n", "TOKEN=secret")
	f.Add("", "")
	f.Add("not: [valid", "TOKEN=secret")

	f.Fuzz(func(t *testing.T, composeContent, envEntry string) {
		composeEnv := []string{}
		if envEntry != "" {
			composeEnv = append(composeEnv, envEntry)
		}

		risks, err := scanComposeEnvPersistenceRisks(composeContent, composeEnv)
		if err != nil {
			return
		}
		for _, risk := range risks {
			if risk.Service == "" {
				t.Fatalf("expected non-empty service in risk: %+v", risk)
			}
			if risk.Key == "" {
				t.Fatalf("expected non-empty key in risk: %+v", risk)
			}
		}
	})
}
