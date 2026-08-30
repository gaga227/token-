package authz

const ResourceSetting = "setting"

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceSetting,
		LabelKey: "Settings",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "Read settings",
				DescriptionKey: "View system and operation settings.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
