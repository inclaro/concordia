workspace "Recorded" "Recording for export canonicalization" {
    model {
        user = person "User"
        sys = softwareSystem "Shop" {
            api = container "API" "" "Go"
            db = container "DB" "" "Postgres"
        }
        user -> api "Uses" "HTTPS"
        api -> db "Reads" "SQL"
    }
    views {
        container sys "shop-containers" {
            include *
            autolayout lr
        }
    }
}
