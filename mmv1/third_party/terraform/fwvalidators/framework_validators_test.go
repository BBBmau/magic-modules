package fwvalidators_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-provider-google/fwvalidators"
	"github.com/hashicorp/terraform-provider-google/google/acctest"

	transport_tpg "github.com/hashicorp/terraform-provider-google/google/transport"
)

func TestFrameworkProvider_CredentialsValidator(t *testing.T) {
	cases := map[string]struct {
		ConfigValue          types.String
		ExpectedWarningCount int
		ExpectedErrorCount   int
	}{
		"configuring credentials as a path to a credentials JSON file is valid": {
			ConfigValue: types.StringValue(transport_tpg.TestFakeCredentialsPath), // Path to a test fixture
		},
		"configuring credentials as a path to a non-existant file is NOT valid": {
			ConfigValue:        types.StringValue("./this/path/doesnt/exist.json"), // Doesn't exist
			ExpectedErrorCount: 1,
		},
		"configuring credentials as a credentials JSON string is valid": {
			ConfigValue: types.StringValue(acctest.GenerateFakeCredentialsJson("CredentialsValidator")),
		},
		"configuring credentials as an empty string is not valid": {
			ConfigValue:        types.StringValue(""),
			ExpectedErrorCount: 1,
		},
		"leaving credentials unconfigured is valid": {
			ConfigValue: types.StringNull(),
		},
	}

	for tn, tc := range cases {
		t.Run(tn, func(t *testing.T) {
			// Arrange
			req := validator.StringRequest{
				ConfigValue: tc.ConfigValue,
			}

			resp := validator.StringResponse{
				Diagnostics: diag.Diagnostics{},
			}

			cv := fwvalidators.CredentialsValidator()

			// Act
			cv.ValidateString(context.Background(), req, &resp)

			// Assert
			if resp.Diagnostics.WarningsCount() > tc.ExpectedWarningCount {
				t.Errorf("Expected %d warnings, got %d", tc.ExpectedWarningCount, resp.Diagnostics.WarningsCount())
			}
			if resp.Diagnostics.ErrorsCount() > tc.ExpectedErrorCount {
				t.Errorf("Expected %d errors, got %d", tc.ExpectedErrorCount, resp.Diagnostics.ErrorsCount())
			}
		})
	}
}

func TestServiceAccountNameValidator(t *testing.T) {
	t.Parallel()

	type testCase struct {
		value         types.String
		expectError   bool
		errorContains string
	}

	tests := map[string]testCase{
		"correct service account name": {
			value:       types.StringValue("test@test.iam.gserviceaccount.com"),
			expectError: false,
		},
		"incorrect service account name": {
			value:         types.StringValue("test"),
			expectError:   true,
			errorContains: "Service account name must be in the format: name@project.iam.gserviceaccount.com",
		},
		"empty string": {
			value:         types.StringValue(""),
			expectError:   true,
			errorContains: "Service account name must not be empty",
		},
		"null value": {
			value:       types.StringNull(),
			expectError: false,
		},
		"unknown value": {
			value:       types.StringUnknown(),
			expectError: false,
		},
	}

	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := validator.StringRequest{
				Path:           path.Root("test"),
				PathExpression: path.MatchRoot("test"),
				ConfigValue:    test.value,
			}
			response := validator.StringResponse{}
			validator := fwvalidators.ServiceAccountNameValidator{}

			validator.ValidateString(context.Background(), request, &response)

			if test.expectError && !response.Diagnostics.HasError() {
				t.Errorf("expected error, got none")
			}

			if !test.expectError && response.Diagnostics.HasError() {
				t.Errorf("got unexpected error: %s", response.Diagnostics.Errors())
			}

			if test.errorContains != "" {
				foundError := false
				for _, err := range response.Diagnostics.Errors() {
					if err.Detail() == test.errorContains {
						foundError = true
						break
					}
				}
				if !foundError {
					t.Errorf("expected error with summary %q, got none", test.errorContains)
				}
			}
		})
	}
}

func TestDurationValidator(t *testing.T) {
	t.Parallel()

	type testCase struct {
		value         types.String
		expectError   bool
		errorContains string
	}

	tests := map[string]testCase{
		"valid duration under max": {
			value:       types.StringValue("1800s"),
			expectError: false,
		},
		"valid duration at max": {
			value:       types.StringValue("3600s"),
			expectError: false,
		},
		"valid duration with different unit": {
			value:       types.StringValue("1h"),
			expectError: false,
		},
		"duration exceeds max - seconds": {
			value:         types.StringValue("7200s"),
			expectError:   true,
			errorContains: "Duration Too Long",
		},
		"duration exceeds max - minutes": {
			value:         types.StringValue("120m"),
			expectError:   true,
			errorContains: "Duration Too Long",
		},
		"duration exceeds max - hours": {
			value:         types.StringValue("2h"),
			expectError:   true,
			errorContains: "Duration Too Long",
		},
		"invalid duration format": {
			value:         types.StringValue("invalid"),
			expectError:   true,
			errorContains: "Invalid Duration Format",
		},
		"empty string": {
			value:         types.StringValue(""),
			expectError:   true,
			errorContains: "Invalid Duration Format",
		},
	}

	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := validator.StringRequest{
				Path:           path.Root("test"),
				PathExpression: path.MatchRoot("test"),
				ConfigValue:    test.value,
			}
			response := validator.StringResponse{}
			validator := fwvalidators.DurationValidator{}

			validator.ValidateString(context.Background(), request, &response)

			if test.expectError && !response.Diagnostics.HasError() {
				t.Errorf("expected error, got none")
			}

			if !test.expectError && response.Diagnostics.HasError() {
				t.Errorf("got unexpected error: %s", response.Diagnostics.Errors())
			}

			if test.errorContains != "" {
				foundError := false
				for _, err := range response.Diagnostics.Errors() {
					if err.Summary() == test.errorContains {
						foundError = true
						break
					}
				}
				if !foundError {
					t.Errorf("expected error with summary %q, got none", test.errorContains)
				}
			}
		})
	}
}

func TestServiceScopeValidator(t *testing.T) {
	t.Parallel()

	type testCase struct {
		value       types.String
		expectError bool
	}

	tests := map[string]testCase{
		"canonical form": {
			value:       types.StringValue("https://www.googleapis.com/auth/cloud-platform"),
			expectError: false,
		},
		"non-canonical form": {
			value:       types.StringValue("cloud-platform"),
			expectError: false,
		},
		"empty string": {
			value:       types.StringValue(""),
			expectError: false,
		},
	}

	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := validator.StringRequest{
				Path:           path.Root("test"),
				PathExpression: path.MatchRoot("test"),
				ConfigValue:    test.value,
			}
			response := validator.StringResponse{}
			validator := fwvalidators.ServiceScopeValidator{}

			validator.ValidateString(context.Background(), request, &response)

			if test.expectError && !response.Diagnostics.HasError() {
				t.Errorf("expected error, got none")
			}

			if !test.expectError && response.Diagnostics.HasError() {
				t.Errorf("got unexpected error: %s", response.Diagnostics.Errors())
			}
		})
	}
}