# YAML config with environment secret override

The Agent Service will load non-secret runtime settings from a YAML config file and read the model API key from an environment variable override. This keeps local setup simple while avoiding the habit of committing credentials into repository files.
