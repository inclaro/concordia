terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.12.0"
    }
  }
}

# A fake access token comes from GOOGLE_OAUTH_ACCESS_TOKEN: the plan is made offline and never applied.
provider "google" {
  project      = "concordia-fake-project"
  region       = "europe-west1"
  zone         = "europe-west1-b"
}
