package authz

const ResourceUser = "user"

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceUser,
		LabelKey: "User Management",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "Read users",
				DescriptionKey: "View user lists and details.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
			{
				Action:         ActionWrite,
				LabelKey:       "Write users",
				DescriptionKey: "Create, edit, disable, or delete users.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
