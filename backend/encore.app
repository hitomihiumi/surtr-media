{
	"id": "",
	"lang": "go",
	"build": {
		"docker": {
			"bundle_source": true,
			"base_image": "debian:bookworm-slim"
		}
	},
	"global_cors": {
		"allow_origins_without_credentials": ["*"],
		"allow_headers": ["Authorization", "Content-Type"]
	}
}
