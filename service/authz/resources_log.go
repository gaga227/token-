package authz

const ResourceLog = "log"

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceLog,
		LabelKey: "Log Management",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "Read logs",
				DescriptionKey: "View usage and error logs.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
			{
				Action:         ActionOperate,
				LabelKey:       "Operate logs",
				DescriptionKey: "Clear or manage logs.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
