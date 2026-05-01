package main

import (
	"reflect"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

func TestParseCallerFlags(t *testing.T) {
	cases := []struct {
		name       string
		in         []string
		wantPos    []string
		wantCaller domain.CallerRef
	}{
		{
			"no flags",
			[]string{"list"},
			[]string{"list"},
			domain.CallerRef{},
		},
		{
			"equals form",
			[]string{"list", "--org=org-x", "--team=eng"},
			[]string{"list"},
			domain.CallerRef{OrgID: "org-x", TeamID: "eng"},
		},
		{
			"space form",
			[]string{"--org", "org-x", "show", "send", "--team", "eng"},
			[]string{"show", "send"},
			domain.CallerRef{OrgID: "org-x", TeamID: "eng"},
		},
		{
			"interleaved",
			[]string{"show", "--org=org-x", "send_message"},
			[]string{"show", "send_message"},
			domain.CallerRef{OrgID: "org-x"},
		},
		{
			"unknown flag passes through",
			[]string{"list", "--unknown"},
			[]string{"list", "--unknown"},
			domain.CallerRef{},
		},
		{
			"only flags",
			[]string{"--org=org-y"},
			[]string{},
			domain.CallerRef{OrgID: "org-y"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pos, caller := parseCallerFlags(tc.in)
			if !reflect.DeepEqual(pos, tc.wantPos) {
				t.Errorf("positional=%v want %v", pos, tc.wantPos)
			}
			if caller != tc.wantCaller {
				t.Errorf("caller=%+v want %+v", caller, tc.wantCaller)
			}
		})
	}
}
