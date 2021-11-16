package config

import (
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		desc     string
		config   *configFile
		mustFail bool
	}{
		{
			desc: "peer with bfd profile",
			config: &configFile{
				Peers: []peer{
					{
						Addr:       "1.2.3.4",
						BFDProfile: "foo",
					},
				},
			},
			mustFail: true,
		},
		{
			desc: "bfd profile set",
			config: &configFile{
				Peers: []peer{
					{
						Addr: "1.2.3.4",
					},
				},
				BFDProfiles: []bfdProfile{
					{
						Name: "default",
					},
				},
			},
			mustFail: true,
		},
		{
			desc: "should pass",
			config: &configFile{
				Peers: []peer{
					{
						Addr: "2001:db8::/64",
					},
				},
			},
			mustFail: false,
		},
		{
			desc: "should pass",
			config: &configFile{
				Peers: []peer{
					{
						Addr: "1.2.3.4",
					},
				},
			},
			mustFail: false,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			err := DiscardFRROnly(test.config)
			if test.mustFail && err == nil {
				t.Fatalf("Expected error for %s", test.desc)
			}
			if !test.mustFail && err != nil {
				t.Fatalf("Not expected error %s for %s", err, test.desc)
			}
		})
	}
}
