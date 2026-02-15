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
	    "debug": true,
		"allow_origins_without_credentials": ["*"],
		"allow_origins_with_credentials": [
                   "http://localhost:3000",
                   "http://192.168.1.232:3000"
               ],
		"allow_headers": ["Authorization", "Content-Type"]
	}
}
