package authz

const ResourceDashboard = "dashboard"

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceDashboard,
		LabelKey: "Dashboard",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "Read dashboard",
				DescriptionKey: "View dashboard statistics and charts.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
