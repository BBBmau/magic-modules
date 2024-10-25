// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0
package resourcemanager

import (
	"context"
	"fmt"

	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-provider-google/google/fwtransport"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"
)

const (
	userInfoScope = "https://www.googleapis.com/auth/userinfo.email"
)

var _ ephemeral.EphemeralResource = &googleEphemeralServiceAccountIdToken{}

func GoogleEphemeralServiceAccountIdToken() ephemeral.EphemeralResource {
	return &googleEphemeralServiceAccountIdToken{}
}

type googleEphemeralServiceAccountIdToken struct {
	providerConfig *fwtransport.FrameworkProviderConfig
}

func (p *googleEphemeralServiceAccountIdToken) Metadata(ctx context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = "google_test"
}

type ephemeralServiceAccountIdTokenModel struct {
	TargetAudience       types.String `tfsdk:"target_audience"`
	TargetServiceAccount types.String `tfsdk:"target_service_account"`
	Delegates            types.Set    `tfsdk:"delegates"`
	IncludeEmail         types.Bool   `tfsdk:"include_email"`
	IdToken              types.String `tfsdk:"id_token"`
}

func (p *googleEphemeralServiceAccountIdToken) Schema(ctx context.Context, req ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"target_audience": {
				Type:     schema.TypeString,
				Required: true,
			},
			"target_service_account": {
				Type:     schema.TypeString,
				Optional: true,
				//ValidateFunc: verify.ValidateRegexp("(" + strings.Join(verify.PossibleServiceAccountNames, "|") + ")"),
			},
			"delegates": {
				Type:        schema.TypeSet,
				Optional:    true,
				ElementType: types.StringType,
				// Validators: verify.ValidateDuration(), // duration <=3600s; TODO: support validateDuration(min,max)
				// Default:      "3600s",
			},
			"include_email": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  basetypes.BoolValue(false),
				//ValidateFunc: verify.ValidateRegexp("(" + strings.Join(verify.PossibleServiceAccountNames, "|") + ")"),
			},
			"id_token": {
				Type:      schema.TypeString,
				Computed:  true,
				Sensitive: true,
			},
		},
	}
}

func (p *googleEphemeralServiceAccountIdToken) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*fwtransport.FrameworkProviderConfig)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *fwtransport.FrameworkProviderConfig, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	// Required for accessing userAgent and passing as an argument into a util function
	p.providerConfig = pd
}

func (p *googleEphemeralServiceAccountIdToken) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data ephemeralServiceAccountIdTokenModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	targetAudience := data.TargetAudience.ValueString()
	creds, err := p.providerConfig.GetCredentials([]string{userInfoScope}, false)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error calling getCredentials()",
			err.Error(),
		)
		return
	}

	targetServiceAccount := data.TargetServiceAccount.ValueString()
	// If a target service account is provided, use the API to generate the idToken
	if targetServiceAccount != "" {
		service := p.providerConfig.NewIamCredentialsClient(p.providerConfig.UserAgent)
		name := fmt.Sprintf("projects/-/serviceAccounts/%s", targetServiceAccount)
		DelegatesSetValue, _ := data.Delegates.ToSetValue(ctx)
		tokenRequest := &iamcredentials.GenerateIdTokenRequest{
			Audience:     targetAudience,
			IncludeEmail: data.IncludeEmail.ValueBool(),
			Delegates:    StringSet(DelegatesSetValue),
		}
		at, err := service.Projects.ServiceAccounts.GenerateIdToken(name, tokenRequest).Do()
		if err != nil {
			resp.Diagnostics.AddError(
				"Error calling iamcredentials.GenerateIdToken",
				err.Error(),
			)
			return
		}

		data.IdToken = types.StringValue(at.Token)
		resp.Diagnostics.Append(resp.Result.Set(ctx, data)...)
		return
	}

	// If no target service account, use the default credentials
	ctx = context.Background()
	co := []option.ClientOption{}
	if creds.JSON != nil {
		co = append(co, idtoken.WithCredentialsJSON(creds.JSON))
	}

	idTokenSource, err := idtoken.NewTokenSource(ctx, targetAudience, co...)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to retrieve TokenSource",
			err.Error(),
		)
		return
	}
	idToken, err := idTokenSource.Token()
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to retrieve Token",
			err.Error(),
		)
		return
	}

	data.IdToken = types.StringValue(idToken.AccessToken)
	resp.Diagnostics.Append(resp.Result.Set(ctx, data)...)
}
