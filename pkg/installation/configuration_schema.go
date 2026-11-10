package installation

import (
	"cmp"
	"reflect"

	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/refl"

	"github.com/holon-sh/protocol"
)

var configurationSchemaNames = map[reflect.Type]string{
	reflect.TypeFor[protocol.AppAccess]():         "CrdAppAccess",
	reflect.TypeFor[protocol.AccessRule]():        "CrdAccessRule",
	reflect.TypeFor[protocol.OAuthRequirement]():  "CrdOAuthRequirement",
	reflect.TypeFor[protocol.Limits]():            "CrdLimits",
	reflect.TypeFor[protocol.Limit]():             "CrdLimit",
	reflect.TypeFor[protocol.Egress]():            "CrdEgress",
	reflect.TypeFor[protocol.EgressDestination](): "CrdEgressDestination",
	reflect.TypeFor[protocol.RegistrySource]():    "ContractRegistrySource",
	reflect.TypeFor[protocol.PackageSelection]():  "ContractPackageSelection",
	reflect.TypeFor[protocol.RegistryRef]():       "McpregistryReference",
}

func configurationReflector() jsonschema.Reflector {
	reflector := jsonschema.Reflector{DefaultOptions: []func(*jsonschema.ReflectContext){
		jsonschema.InterceptDefName(func(t reflect.Type, name string) string {
			return cmp.Or(configurationSchemaNames[t], name)
		}),
		jsonschema.InterceptProp(func(p jsonschema.InterceptPropParams) error {
			if p.Processed && p.PropertySchema.Items != nil {
				return refl.PopulateFieldsFromTags(p.PropertySchema.Items.SchemaOrBool.TypeObject, p.Field.Tag, func(o *refl.FieldsFromTagsOptions) {
					o.TagPrefix = "items."
				})
			}
			return nil
		}),
		jsonschema.InterceptSchema(func(p jsonschema.InterceptSchemaParams) (bool, error) {
			if !p.Processed {
				return false, nil
			}
			t := refl.DeepIndirect(p.Value.Type())
			if t.Kind() == reflect.Struct && (t.Name() != "" || len(p.Schema.Properties) == 0) {
				p.Schema.AdditionalProperties = &jsonschema.SchemaOrBool{TypeBoolean: new(false)}
			}
			return false, nil
		}),
	}}
	return reflector
}
