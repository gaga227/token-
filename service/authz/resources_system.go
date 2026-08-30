package authz

const ResourceSystem = "system"

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceSystem,
		LabelKey: "System",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "Read system info",
				DescriptionKey: "View system status and instance information.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
