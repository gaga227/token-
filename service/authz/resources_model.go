package authz

const ResourceModel = "model"

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceModel,
		LabelKey: "Model Management",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "Read models",
				DescriptionKey: "View model lists and documentation.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
			{
				Action:         ActionWrite,
				LabelKey:       "Write models",
				DescriptionKey: "Create, edit, or delete model entries.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
