package authz

const ResourceRedemption = "redemption"

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceRedemption,
		LabelKey: "Redemption Codes",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "Read redemption codes",
				DescriptionKey: "View redemption code lists.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
