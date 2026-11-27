# Payments on Azure: an app VM reaches a ledger private endpoint and a ledger NIC through NSGs.

resource "azurerm_resource_group" "main" {
  name     = "payments"
  location = "westeurope"
}

resource "azurerm_virtual_network" "main" {
  name                = "payments"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  address_space       = ["10.40.0.0/16"]
}

resource "azurerm_subnet" "app" {
  name                 = "app"
  resource_group_name  = azurerm_resource_group.main.name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = ["10.40.1.0/24"]
}

resource "azurerm_subnet" "data" {
  name                              = "data"
  resource_group_name               = azurerm_resource_group.main.name
  virtual_network_name              = azurerm_virtual_network.main.name
  address_prefixes                  = ["10.40.2.0/24"]
  private_endpoint_network_policies = "Enabled"
}

resource "azurerm_application_security_group" "app" {
  name                = "app"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
}

resource "azurerm_network_security_group" "data" {
  name                = "data"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name

  security_rule {
    name                                  = "AllowPostgresFromApp"
    priority                              = 100
    direction                             = "Inbound"
    access                                = "Allow"
    protocol                              = "Tcp"
    source_port_range                     = "*"
    destination_port_range                = "5432"
    source_application_security_group_ids = [azurerm_application_security_group.app.id]
    destination_address_prefix            = "*"
  }

  security_rule {
    name                       = "DenyVnetInbound"
    priority                   = 4000
    direction                  = "Inbound"
    access                     = "Deny"
    protocol                   = "*"
    source_port_range          = "*"
    destination_port_range     = "*"
    source_address_prefix      = "VirtualNetwork"
    destination_address_prefix = "*"
  }
}

resource "azurerm_network_security_rule" "data_https" {
  name                        = "AllowHttpsFromAppSubnet"
  priority                    = 200
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "443"
  source_address_prefix       = "10.40.1.0/24"
  destination_address_prefix  = "*"
  resource_group_name         = azurerm_resource_group.main.name
  network_security_group_name = azurerm_network_security_group.data.name
}

resource "azurerm_subnet_network_security_group_association" "data" {
  subnet_id                 = azurerm_subnet.data.id
  network_security_group_id = azurerm_network_security_group.data.id
}

resource "azurerm_network_interface" "app" {
  name                = "app"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name

  ip_configuration {
    name                          = "primary"
    subnet_id                     = azurerm_subnet.app.id
    private_ip_address_allocation = "Static"
    private_ip_address            = "10.40.1.10"
  }
}

resource "azurerm_network_interface_application_security_group_association" "app" {
  network_interface_id          = azurerm_network_interface.app.id
  application_security_group_id = azurerm_application_security_group.app.id
}

resource "azurerm_network_interface" "batch" {
  name                = "batch"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name

  ip_configuration {
    name                          = "primary"
    subnet_id                     = azurerm_subnet.app.id
    private_ip_address_allocation = "Static"
    private_ip_address            = "10.40.1.20"
  }
}

resource "azurerm_network_interface" "ledger" {
  name                = "ledger"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name

  ip_configuration {
    name                          = "primary"
    subnet_id                     = azurerm_subnet.data.id
    private_ip_address_allocation = "Static"
    private_ip_address            = "10.40.2.10"
  }
}

resource "azurerm_linux_virtual_machine" "app" {
  name                            = "app"
  location                        = azurerm_resource_group.main.location
  resource_group_name             = azurerm_resource_group.main.name
  size                            = "Standard_B1s"
  admin_username                  = "payments"
  disable_password_authentication = true
  network_interface_ids           = [azurerm_network_interface.app.id]

  admin_ssh_key {
    username   = "payments"
    public_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINwkA0ezWs1xWo33zK52Pff+cJotV8vaexyl9HPw9vUG fake"
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "ubuntu-24_04-lts"
    sku       = "server"
    version   = "latest"
  }
}

resource "azurerm_storage_account" "ledger" {
  name                     = "concordialedgerfake"
  location                 = azurerm_resource_group.main.location
  resource_group_name      = azurerm_resource_group.main.name
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_private_endpoint" "ledger" {
  name                = "ledger"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  subnet_id           = azurerm_subnet.data.id

  private_service_connection {
    name                           = "ledger"
    private_connection_resource_id = azurerm_storage_account.ledger.id
    subresource_names              = ["blob"]
    is_manual_connection           = false
  }
}
