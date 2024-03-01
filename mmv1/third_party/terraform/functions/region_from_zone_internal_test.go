// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0
package functions

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func TestFunctionRun_region_from_zone(t *testing.T) {
	t.Parallel()

	region := "us-central1"

	testCases := map[string]struct {
		request  function.RunRequest
		expected function.RunResponse
	}{
		"it returns the expected output value when given a valid zone input": {
			request: function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{types.StringValue("us-central1-b")}),
			},
			expected: function.RunResponse{
				Result: function.NewResultData(types.StringValue(region)),
			},
		},
		"it returns an error when given input is empty": {
			request: function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{types.StringValue("")}),
			},
			expected: function.RunResponse{
				Result: function.NewResultData(types.StringNull()),
				Diagnostics: diag.Diagnostics{
					diag.NewArgumentErrorDiagnostic(
						0,
						noMatchesErrorSummary,
						"The input string is empty.",
					),
				},
			},
		},
		"it returns an error when given input is not a zone": {
			request: function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{types.StringValue("foobar")}),
			},
			expected: function.RunResponse{
				Result: function.NewResultData(types.StringNull()),
				Diagnostics: diag.Diagnostics{
					diag.NewArgumentErrorDiagnostic(
						0,
						noMatchesErrorSummary,
						"The input string is invalid.",
					),
				},
			},
		},
	}

	for name, testCase := range testCases {
		tn, tc := name, testCase

		t.Run(tn, func(t *testing.T) {
			t.Parallel()

			// Arrange
			got := function.RunResponse{
				Result: function.NewResultData(basetypes.StringValue{}),
			}

			// Act
			NewRegionFromZoneFunction().Run(context.Background(), tc.request, &got)

			// Assert
			if diff := cmp.Diff(got.Result, tc.expected.Result); diff != "" {
				t.Errorf("unexpected diff between expected and received result: %s", diff)
			}
			if diff := cmp.Diff(got.Diagnostics, tc.expected.Diagnostics); diff != "" {
				t.Errorf("unexpected diff between expected and received diagnostics: %s", diff)
			}
		})
	}
}
