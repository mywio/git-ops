package main

import "testing"

func FuzzParseHealthContainers(f *testing.F) {
	for _, seed := range []string{
		"",
		"[]",
		`{"Service":"api","State":"running"}`,
		`[{"Service":"api","State":"running"},{"Service":"db","State":"exited"}]`,
		`not-json`,
		`{"broken":`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		containers, ok := parseHealthContainers(input)
		if !ok {
			return
		}
		if containers == nil {
			t.Fatalf("ok=true with nil containers")
		}
	})
}
