package authz

const ResourceToken = "token"

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceToken,
		LabelKey: "Token Management",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "Read tokens",
				DescriptionKey: "View API token lists and details.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
			{
				Action:         ActionWrite,
				LabelKey:       "Write tokens",
				DescriptionKey: "Create, edit, and delete API tokens.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
