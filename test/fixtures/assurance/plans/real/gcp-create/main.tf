# Payments on GCP: an app instance reaches a ledger instance through VPC firewall rules.

resource "google_compute_network" "main" {
  name                    = "payments"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "app" {
  name          = "app"
  network       = google_compute_network.main.id
  ip_cidr_range = "10.30.1.0/24"
}

# A dual-stack subnetwork.
resource "google_compute_subnetwork" "data" {
  name             = "data"
  network          = google_compute_network.main.id
  ip_cidr_range    = "10.30.2.0/24"
  stack_type       = "IPV4_IPV6"
  ipv6_access_type = "INTERNAL"
}

resource "google_service_account" "ledger" {
  account_id = "ledger"
}

resource "google_compute_instance" "app" {
  name         = "app"
  machine_type = "e2-small"
  tags         = ["app"]

  boot_disk {
    initialize_params {
      image = "debian-cloud/debian-12"
    }
  }

  network_interface {
    network    = google_compute_network.main.id
    subnetwork = google_compute_subnetwork.app.id
    network_ip = "10.30.1.10"
  }

  service_account {
    email  = "app@concordia-fake-project.iam.gserviceaccount.com"
    scopes = ["cloud-platform"]
  }
}

resource "google_compute_instance" "ledger" {
  name         = "ledger"
  machine_type = "e2-small"
  tags         = ["ledger"]

  boot_disk {
    initialize_params {
      image = "debian-cloud/debian-12"
    }
  }

  network_interface {
    network    = google_compute_network.main.id
    subnetwork = google_compute_subnetwork.data.id
    network_ip = "10.30.2.10"
  }

  service_account {
    email  = google_service_account.ledger.email
    scopes = ["cloud-platform"]
  }
}

resource "google_compute_instance" "batch" {
  name         = "batch"
  machine_type = "e2-small"
  tags         = ["batch"]

  boot_disk {
    initialize_params {
      image = "debian-cloud/debian-12"
    }
  }

  network_interface {
    network    = google_compute_network.main.id
    subnetwork = google_compute_subnetwork.app.id
    network_ip = "10.30.1.20"
  }
}

resource "google_compute_firewall" "ledger_from_app" {
  name        = "ledger-from-app"
  network     = google_compute_network.main.id
  direction   = "INGRESS"
  priority    = 1000
  source_tags = ["app"]
  target_tags = ["ledger"]

  allow {
    protocol = "tcp"
    ports    = ["5432"]
  }
}

resource "google_compute_firewall" "deny_batch_ssh" {
  name          = "deny-batch-ssh"
  network       = google_compute_network.main.id
  direction     = "INGRESS"
  priority      = 900
  source_ranges = ["10.30.1.0/24"]
  target_tags   = ["batch"]

  deny {
    protocol = "tcp"
    ports    = ["22"]
  }
}

resource "google_compute_firewall" "allow_batch_ssh" {
  name          = "allow-batch-ssh"
  network       = google_compute_network.main.id
  direction     = "INGRESS"
  priority      = 900
  source_ranges = ["10.30.0.0/16"]
  target_tags   = ["batch"]

  allow {
    protocol = "tcp"
    ports    = ["22", "8000-8100"]
  }
}

resource "google_compute_firewall" "app_egress_deny_ledger" {
  name               = "app-egress-deny-ledger-admin"
  network            = google_compute_network.main.id
  direction          = "EGRESS"
  priority           = 800
  destination_ranges = ["10.30.2.10/32"]
  target_tags        = ["app"]

  deny {
    protocol = "tcp"
    ports    = ["9000"]
  }
}

resource "google_compute_firewall" "ledger_metrics" {
  name                    = "ledger-metrics"
  network                 = google_compute_network.main.id
  direction               = "INGRESS"
  source_service_accounts = ["app@concordia-fake-project.iam.gserviceaccount.com"]
  target_service_accounts = [google_service_account.ledger.email]

  allow {
    protocol = "tcp"
    ports    = ["9100"]
  }
}
