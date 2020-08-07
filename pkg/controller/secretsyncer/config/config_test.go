package config

import "testing"

func TestValidate(t *testing.T) {
	var testCases = []struct {
		name        string
		config      Configuration
		expectedErr bool
	}{
		{
			name:        "empty config is invalid",
			config:      Configuration{},
			expectedErr: true,
		},
		{
			name: "config with nothing missing is valid",
			config: Configuration{Secrets: []MirrorConfig{
				{
					From: SecretLocation{Namespace: "from-ns", Name: "from-name"},
					To:   SecretLocation{Namespace: "to-ns", Name: "to-name"},
				},
			}},
			expectedErr: false,
		},
		{
			name: "config with from ns missing is invalid",
			config: Configuration{Secrets: []MirrorConfig{
				{
					From: SecretLocation{Name: "from-name"},
					To:   SecretLocation{Namespace: "to-ns", Name: "to-name"},
				},
			}},
			expectedErr: true,
		},
		{
			name: "config with from name missing is invalid",
			config: Configuration{Secrets: []MirrorConfig{
				{
					From: SecretLocation{Namespace: "from-ns"},
					To:   SecretLocation{Namespace: "to-ns", Name: "to-name"},
				},
			}},
			expectedErr: true,
		},
		{
			name: "config with to ns missing is invalid",
			config: Configuration{Secrets: []MirrorConfig{
				{
					From: SecretLocation{Namespace: "from-ns", Name: "from-name"},
					To:   SecretLocation{Name: "to-name"},
				},
			}},
			expectedErr: true,
		},
		{
			name: "config with to name missing is invalid",
			config: Configuration{Secrets: []MirrorConfig{
				{
					From: SecretLocation{Namespace: "from-ns", Name: "from-name"},
					To:   SecretLocation{Namespace: "to-ns"},
				},
			}},
			expectedErr: true,
		},
		{
			name: "config with cycle is invalid",
			config: Configuration{Secrets: []MirrorConfig{
				{
					From: SecretLocation{Namespace: "from-ns", Name: "from-name"},
					To:   SecretLocation{Namespace: "to-ns", Name: "to-name"},
				},
				{
					From: SecretLocation{Namespace: "to-ns", Name: "to-name"},
					To:   SecretLocation{Namespace: "from-ns", Name: "from-name"},
				},
			}},
			expectedErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.config.Validate()
			if err == nil && testCase.expectedErr {
				t.Errorf("%s: expected an error but got none", testCase.name)
			}
			if err != nil && !testCase.expectedErr {
				t.Errorf("%s: expected no error but got one: %v", testCase.name, err)
			}
		})
	}
}
