package homekit

import "encoding/json"

// These schemas use the SDK's bounded JSON Schema profile.
const (
	emptyInputSchema     = `{"type":"object","additionalProperties":false}`
	assignmentSchema     = `{"type":"object","additionalProperties":false,"required":["entity_id","role"],"properties":{"entity_id":{"type":"string","minLength":1,"maxLength":256},"role":{"type":"string","minLength":1,"maxLength":64},"inverted":{"type":"boolean"}}}`
	namedEntitySchema    = `{"type":"object","additionalProperties":false,"required":["entity_id","name"],"properties":{"entity_id":{"type":"string"},"name":{"type":"string"}}}`
	accessorySchema      = `{"type":"object","additionalProperties":false,"required":["device_id","name","kind","aid","status","entities"],"properties":{"device_id":{"type":"string"},"name":{"type":"string"},"kind":{"type":"string"},"aid":{"type":"integer","minimum":2},"status":{"enum":["reachable","partially_unreachable","unreachable","missing"]},"entities":{"type":"array","maxItems":512,"items":` + assignmentSchema + `}}}`
	unresolvedSchema     = `{"type":"object","additionalProperties":false,"required":["role","reason","candidates"],"properties":{"role":{"type":"string"},"reason":{"enum":["missing_required","ambiguous","class_required"]},"candidates":{"type":"array","maxItems":512,"items":` + namedEntitySchema + `}}}`
	candidateSchema      = `{"type":"object","additionalProperties":false,"required":["device_id","name","device_kind","allowed_kinds","proposed","unresolved","unmapped"],"properties":{"device_id":{"type":"string"},"name":{"type":"string"},"device_kind":{"type":"string"},"default_kind":{"type":"string"},"allowed_kinds":{"type":"array","maxItems":512,"items":{"type":"string"}},"proposed":{"type":"array","maxItems":512,"items":` + assignmentSchema + `},"unresolved":{"type":"array","maxItems":512,"items":` + unresolvedSchema + `},"unmapped":{"type":"array","maxItems":512,"items":` + namedEntitySchema + `}}}`
	listOutputSchema     = `{"type":"object","additionalProperties":false,"required":["paired","accessory_count","accessory_limit","accessories","candidates","truncated"],"properties":{"paired":{"type":"boolean"},"accessory_count":{"type":"integer","minimum":0},"accessory_limit":{"const":149},"accessories":{"type":"array","maxItems":512,"items":` + accessorySchema + `},"candidates":{"type":"array","maxItems":512,"items":` + candidateSchema + `},"truncated":{"type":"boolean"}}}`
	exposeInputSchema    = `{"type":"object","additionalProperties":false,"required":["device_id"],"properties":{"device_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"enum":["lightbulb","switch","outlet","lock","window_covering","thermostat","air_purifier","sensor"]},"name":{"type":"string","maxLength":512},"entities":{"type":"array","maxItems":512,"items":` + assignmentSchema + `}}}`
	unexposeInputSchema  = `{"type":"object","additionalProperties":false,"required":["device_id"],"properties":{"device_id":{"type":"string","minLength":1,"maxLength":256}}}`
	unexposeOutputSchema = `{"type":"object","additionalProperties":false,"required":["device_id","status"],"properties":{"device_id":{"type":"string"},"status":{"enum":["unexposed","not_exposed"]}}}`
	pairingOutputSchema  = `{"type":"object","additionalProperties":false,"required":["paired"],"properties":{"paired":{"type":"boolean"},"controller_count":{"type":"integer","minimum":1},"setup_code":{"type":"string"},"setup_uri":{"type":"string"}}}`
	resetInputSchema     = `{"type":"object","additionalProperties":false,"required":["confirm"],"properties":{"confirm":{"const":"reset_all_homekit_pairings"}}}`
)

func schemaBytes(schema string) json.RawMessage { return json.RawMessage(schema) }
