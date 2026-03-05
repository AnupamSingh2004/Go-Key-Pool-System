package util

import "encoding/json"

func ParseJSONMap(jsonStr string) map[string]string {
result := make(map[string]string)
if jsonStr == "" || jsonStr == "{}" {
return result
}
if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
return make(map[string]string)
}
return result
}
