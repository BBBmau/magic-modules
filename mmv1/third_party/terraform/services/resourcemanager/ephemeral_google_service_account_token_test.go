package resourcemanager_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-provider-google/google/acctest"
	"github.com/hashicorp/terraform-provider-google/google/envvar"
	"github.com/hashicorp/terraform-provider-google/google/services/resourcemanager"
)

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
			errorContains: "Service account name must match one of the expected patterns for Google service accounts",
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
			validator := resourcemanager.ServiceAccountNameValidator{}

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
		minDuration   time.Duration
		maxDuration   time.Duration
		expectError   bool
		errorContains string
	}

	tests := map[string]testCase{
		"valid duration between min and max": {
			value:       types.StringValue("1800s"),
			minDuration: time.Hour / 2,
			maxDuration: time.Hour,
			expectError: false,
		},
		"valid duration at min": {
			value:       types.StringValue("1800s"),
			minDuration: 30 * time.Minute,
			maxDuration: time.Hour,
			expectError: false,
		},
		"valid duration at max": {
			value:       types.StringValue("3600s"),
			minDuration: time.Hour / 2,
			maxDuration: time.Hour,
			expectError: false,
		},
		"valid duration with different unit": {
			value:       types.StringValue("1h"),
			minDuration: 30 * time.Minute,
			maxDuration: 2 * time.Hour,
			expectError: false,
		},
		"duration below min": {
			value:         types.StringValue("900s"),
			minDuration:   30 * time.Minute,
			maxDuration:   time.Hour,
			expectError:   true,
			errorContains: "Duration Too Short",
		},
		"duration exceeds max - seconds": {
			value:         types.StringValue("7200s"),
			minDuration:   30 * time.Minute,
			maxDuration:   time.Hour,
			expectError:   true,
			errorContains: "Duration Too Long",
		},
		"duration exceeds max - minutes": {
			value:         types.StringValue("120m"),
			minDuration:   30 * time.Minute,
			maxDuration:   time.Hour,
			expectError:   true,
			errorContains: "Duration Too Long",
		},
		"duration exceeds max - hours": {
			value:         types.StringValue("2h"),
			minDuration:   30 * time.Minute,
			maxDuration:   time.Hour,
			expectError:   true,
			errorContains: "Duration Too Long",
		},
		"invalid duration format": {
			value:         types.StringValue("invalid"),
			minDuration:   30 * time.Minute,
			maxDuration:   time.Hour,
			expectError:   true,
			errorContains: "Invalid Duration Format",
		},
		"empty string": {
			value:         types.StringValue(""),
			minDuration:   30 * time.Minute,
			maxDuration:   time.Hour,
			expectError:   true,
			errorContains: "Invalid Duration Format",
		},
		"null value": {
			value:       types.StringNull(),
			minDuration: 30 * time.Minute,
			maxDuration: time.Hour,
			expectError: false,
		},
		"unknown value": {
			value:       types.StringUnknown(),
			minDuration: 30 * time.Minute,
			maxDuration: time.Hour,
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
			validator := resourcemanager.DurationValidator{
				MinDuration: test.minDuration,
				MaxDuration: test.maxDuration,
			}

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
			validator := resourcemanager.ServiceScopeValidator{}

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

func TestAccEphemeralServiceAccountToken_basic(t *testing.T) {
	t.Parallel()

	serviceAccount := envvar.GetTestServiceAccountFromEnv(t)
	targetServiceAccountEmail := acctest.BootstrapServiceAccount(t, "basic", serviceAccount)

	resource.Test(t, resource.TestCase{
		PreCheck: func() { acctest.AccTestPreCheck(t) },
		ExternalProviders: map[string]resource.ExternalProvider{
			"time": {},
		},
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories(t),
		Steps: []resource.TestStep{
			{
				Config: testAccEphemeralServiceAccountToken_basic(targetServiceAccountEmail),
			},
		},
	})
}

func TestAccEphemeralServiceAccountToken_withDelegates(t *testing.T) {
	t.Parallel()

	project := envvar.GetTestProjectFromEnv()
	initialServiceAccount := envvar.GetTestServiceAccountFromEnv(t)
	delegateServiceAccountEmailOne := acctest.BootstrapServiceAccount(t, "delegate1", initialServiceAccount)          // SA_2
	delegateServiceAccountEmailTwo := acctest.BootstrapServiceAccount(t, "delegate2", delegateServiceAccountEmailOne) // SA_3
	targetServiceAccountEmail := acctest.BootstrapServiceAccount(t, "target", delegateServiceAccountEmailTwo)         // SA_4

	resource.Test(t, resource.TestCase{
		PreCheck: func() { acctest.AccTestPreCheck(t) },
		ExternalProviders: map[string]resource.ExternalProvider{
			"time": {},
		},
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories(t),
		Steps: []resource.TestStep{
			{
				Config: testAccEphemeralServiceAccountToken_delegatesSetup(initialServiceAccount, delegateServiceAccountEmailOne, delegateServiceAccountEmailTwo, targetServiceAccountEmail, project),
			},
			{
				Config: testAccEphemeralServiceAccountToken_withDelegates(initialServiceAccount, delegateServiceAccountEmailOne, delegateServiceAccountEmailTwo, targetServiceAccountEmail, project),
			},
		},
	})
}

func TestAccEphemeralServiceAccountToken_withCustomLifetime(t *testing.T) {
	t.Parallel()

	serviceAccount := envvar.GetTestServiceAccountFromEnv(t)
	targetServiceAccountEmail := acctest.BootstrapServiceAccount(t, "lifetime", serviceAccount)

	resource.Test(t, resource.TestCase{
		PreCheck: func() { acctest.AccTestPreCheck(t) },
		ExternalProviders: map[string]resource.ExternalProvider{
			"time": {},
		},
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories(t),
		Steps: []resource.TestStep{
			{
				Config: testAccEphemeralServiceAccountToken_withCustomLifetime(targetServiceAccountEmail),
			},
		},
	})
}

func testAccEphemeralServiceAccountToken_basic(serviceAccountEmail string) string {
	return fmt.Sprintf(`
ephemeral "google_service_account_token" "token" {
  target_service_account = "%s"
  scopes                = ["https://www.googleapis.com/auth/cloud-platform"]
}
`, serviceAccountEmail)
}

func testAccEphemeralServiceAccountToken_withDelegates(initialServiceAccountEmail, delegateServiceAccountEmailOne, delegateServiceAccountEmailTwo, targetServiceAccountEmail, project string) string {
	return fmt.Sprintf(`
resource "google_project_iam_member" "terraform_sa_token_creator" {
  project = "%[5]s"
  role    = "roles/iam.serviceAccountTokenCreator"
  member  = "serviceAccount:%[1]s"
}

resource "time_sleep" "wait_60_seconds" {
  depends_on = [
    google_project_iam_member.terraform_sa_token_creator,
  ]
  create_duration = "60s"
}

ephemeral "google_service_account_token" "test" {
  target_service_account = "%[4]s"
  delegates = [
    "%[3]s",
    "%[2]s",
  ]
  scopes = ["https://www.googleapis.com/auth/cloud-platform"]
  lifetime = "3600s"
}

# The delegation chain is:
# SA_1 (initialServiceAccountEmail) -> SA_2 (delegateServiceAccountEmailOne) -> SA_3 (delegateServiceAccountEmailTwo) -> SA_4 (targetServiceAccountEmail)
`, initialServiceAccountEmail, delegateServiceAccountEmailOne, delegateServiceAccountEmailTwo, targetServiceAccountEmail, project)
}

func testAccEphemeralServiceAccountToken_delegatesSetup(initialServiceAccountEmail, delegateServiceAccountEmailOne, delegateServiceAccountEmailTwo, targetServiceAccountEmail, project string) string {
	return fmt.Sprintf(`
resource "google_project_iam_member" "terraform_sa_token_creator" {
  project = "%[5]s"
  role    = "roles/iam.serviceAccountTokenCreator"
  member  = "serviceAccount:%[1]s"
}

resource "time_sleep" "wait_60_seconds" {
  depends_on = [
    google_project_iam_member.terraform_sa_token_creator,
  ]
  create_duration = "60s"
}

# The delegation chain is:
# SA_1 (initialServiceAccountEmail) -> SA_2 (delegateServiceAccountEmailOne) -> SA_3 (delegateServiceAccountEmailTwo) -> SA_4 (targetServiceAccountEmail)
`, initialServiceAccountEmail, delegateServiceAccountEmailOne, delegateServiceAccountEmailTwo, targetServiceAccountEmail, project)
}

func testAccEphemeralServiceAccountToken_withCustomLifetime(serviceAccountEmail string) string {
	return fmt.Sprintf(`
ephemeral "google_service_account_token" "token" {
  target_service_account = "%s"
  scopes                = ["https://www.googleapis.com/auth/cloud-platform"]
  lifetime              = "3600s"
}
`, serviceAccountEmail)
}
