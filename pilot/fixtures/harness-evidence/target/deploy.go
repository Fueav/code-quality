package deploy

func Region(config map[string]string) string {
	if value := config["region_v2"]; value != "" {
		return value
	}
	return config["region"]
}
