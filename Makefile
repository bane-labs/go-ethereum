# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: geth android ios evm all test clean privnet_init privnet_nodes_stop privnet_bootnode_stop privnet_stop privnet_clean privnet_start

GOBIN = ./build/bin
GO ?= latest
GORUN = go run

MAIN_DIR = ./privnet

NODE1 = node1
NODE1_PORT = 30306
NODE1_RPC_PORT = 8552

NODE2 = node2
NODE2_PORT = 30307
NODE2_RPC_PORT = 8553

PASSWORD_LEN = 32
GENESIS_WORK_JSON = genesis_privnet.json

BOOTNODE = bootnode
BOOTNODE_PORT = 30305
BOOTNODE_LOGLEVEL = 5

RESTRICTED_NETWORK = 127.0.0.0/24
NAT_POLICY = none

define generate_bootnode
	@mkdir -p $(MAIN_DIR)/$(BOOTNODE)
	@$(GOBIN)/bootnode -genkey $(MAIN_DIR)/$(BOOTNODE)/bootnode.key
	@echo $$($(GOBIN)/bootnode --writeaddress -nodekey $(MAIN_DIR)/$(BOOTNODE)/bootnode.key) > $(MAIN_DIR)/$(BOOTNODE)/bootnode_address.txt
endef

define generate_password
   $$(</dev/urandom tr -dc '12345qwertQWERTasdfgASDFGzxcvbZXCVB' | head -c$(PASSWORD_LEN); echo "")
endef

define replace_chainid
	@sed -i "s/_chain_id_/$$(cat $(MAIN_DIR)/networkid.txt)/gI" $(MAIN_DIR)/$(GENESIS_WORK_JSON)
endef

define replace_node_address
	@echo $$(cat $(MAIN_DIR)/$(1)/keystore/* | sed -En 's/.*"address":"([^"]*).*/\1/p') > $(MAIN_DIR)/$(1)/node_address.txt
	@sed -i "s/$(1)/$$(cat $(MAIN_DIR)/$(1)/node_address.txt)/gI" $(MAIN_DIR)/$(GENESIS_WORK_JSON)
endef

define create_account
    @mkdir -p $(MAIN_DIR)/$(1)
    @echo $(call generate_password) > $(MAIN_DIR)/$(1)/password.txt
    @$(GOBIN)/geth --datadir $(MAIN_DIR)/$(1) account new --password $(MAIN_DIR)/$(1)/password.txt
    $(call replace_node_address,$(1))
    @echo "Account $(1): "$$(cat $(MAIN_DIR)/$(1)/node_address.txt)
endef

define run_bootnode
    @$(GOBIN)/bootnode -nodekey $(MAIN_DIR)/$(BOOTNODE)/bootnode.key \
    	-addr :$(BOOTNODE_PORT) \
    	-verbosity $(BOOTNODE_LOGLEVEL) > $(MAIN_DIR)/$(BOOTNODE)/bootnode.log 2>&1 &
endef

define run_miner_node
	$(call run_node,$(1),$(2),$(3),--mine --miner.etherbase="0x$$(cat $(MAIN_DIR)/$(4)/node_address.txt)")
endef

define run_node
	@$(GOBIN)/geth --datadir $(MAIN_DIR)/$(1) \
		--port $(2) \
		--bootnodes "enode://$$(cat $(MAIN_DIR)/$(BOOTNODE)/bootnode_address.txt)@127.0.0.1:0?discport=$(BOOTNODE_PORT)" \
		--networkid "$$(cat $(MAIN_DIR)/networkid.txt)" \
		--unlock 0x"$$(cat $(MAIN_DIR)/$(1)/node_address.txt)" \
		--authrpc.port $(3) \
		--password $(MAIN_DIR)/$(1)/password.txt \
		--metrics \
		--nat $(NAT_POLICY) \
		--netrestrict $(RESTRICTED_NETWORK) \
		$(4) >  $(MAIN_DIR)/$(1)/geth_node.log 2>&1 &
endef

define copy_genesis
	@cp $(MAIN_DIR)/$(GENESIS_WORK_JSON) $(MAIN_DIR)/$(1)/$(GENESIS_WORK_JSON)
endef

define init_node
    @$(GOBIN)/geth init --datadir $(MAIN_DIR)/$(1) $(MAIN_DIR)/$(1)/$(GENESIS_WORK_JSON) > $(MAIN_DIR)/$(1)/geth_init.log 2>&1
endef

define create_json
	@echo "{" > $(MAIN_DIR)/config.json
	@echo "\t\"version\": \"1.0\"," >> $(MAIN_DIR)/config.json
	@echo "\t\"accounts\": [" >> $(MAIN_DIR)/config.json
	@echo "\t\t{" >> $(MAIN_DIR)/config.json
	@echo "\t\t\t\"address\": \""$$(cat $(MAIN_DIR)/$(1)/node_address.txt)\", >> $(MAIN_DIR)/config.json
	@echo "\t\t\t\"key\": \""$$(cat $(MAIN_DIR)/$(1)/password.txt)\" >> $(MAIN_DIR)/config.json
	@echo "\t\t}," >> $(MAIN_DIR)/config.json
	@echo "\t\t{" >> $(MAIN_DIR)/config.json
    @echo "\t\t\t\"address\": \""$$(cat $(MAIN_DIR)/$(2)/node_address.txt)\", >> $(MAIN_DIR)/config.json
    @echo "\t\t\t\"key\": \""$$(cat $(MAIN_DIR)/$(2)/password.txt)\" >> $(MAIN_DIR)/config.json
    @echo "\t\t}" >> $(MAIN_DIR)/config.json
	@echo "\t]," >> $(MAIN_DIR)/config.json
	@echo "\t\"chain_id\": \""$$(cat $(MAIN_DIR)/networkid.txt)\" >> $(MAIN_DIR)/config.json
	@echo "}" >> $(MAIN_DIR)/config.json
endef

geth:
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	@echo "Run \"$(GOBIN)/geth\" to launch geth."

all:
	$(GORUN) build/ci.go install

test: all
	$(GORUN) build/ci.go test

lint: ## Run linters.
	$(GORUN) build/ci.go lint

clean:
	go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

devtools:
	env GOBIN= go install golang.org/x/tools/cmd/stringer@latest
	env GOBIN= go install github.com/fjl/gencodec@latest
	env GOBIN= go install github.com/golang/protobuf/protoc-gen-go@latest
	env GOBIN= go install ./cmd/abigen
	@type "solc" 2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

# Privnet targets

privnet_init: privnet_clean
	@find $(MAIN_DIR)/* -type d -name 'keystore' -exec rm -rf {} +
	@mkdir -p $(MAIN_DIR)
	@echo "Generate  $(GENESIS_WORK_JSON) file"
	@cp $(MAIN_DIR)/genesis_template.json $(MAIN_DIR)/$(GENESIS_WORK_JSON)
	@echo $$(date +'%y%m%d%H%M') > $(MAIN_DIR)/networkid.txt
	@echo "Network ID is "$$(cat $(MAIN_DIR)/networkid.txt)
	@echo "Generate bootnode"
	$(call generate_bootnode)
	$(call replace_chainid)
	@echo "Create accounts"
	$(call create_account,$(NODE1))
	$(call create_account,$(NODE2))
	@echo "Copy genesis_privnet.json into nodes"
	$(call copy_genesis,$(NODE1))
	$(call copy_genesis,$(NODE2))
	@rm $(MAIN_DIR)/$(GENESIS_WORK_JSON)
	@echo "Generate config.json"
	$(call create_json,$(NODE1),$(NODE2))
	@echo "OK! For starting use 'make privnet_start'"

privnet_nodes_stop:
	@echo "Killing nodes processes"
	@killall -w -v -9 geth || :

privnet_bootnode_stop:
	@echo "Killing bootnode processes"
	@killall -w -v -9 bootnode || :

privnet_stop: privnet_bootnode_stop privnet_nodes_stop

privnet_clean: privnet_stop
	@echo "Cleaning the nodes database files from $(MAIN_DIR)"
	@find $(MAIN_DIR)/* -type d -name 'geth' -print -exec rm -rf {} +
	@find $(MAIN_DIR)/* -type s,f -not \( -path '*/keystore/*' -or -name '*.json' -or -name '*.txt' -or -name '*.key' -or -name '*.md' \) -print -exec rm -f {} +

privnet_start:
	@echo "Init nodes"
	$(call init_node,$(NODE1))
	$(call init_node,$(NODE2))
	@echo "Starting nodes..."
	$(call run_bootnode)
	$(call run_miner_node,$(NODE1),$(NODE1_PORT),$(NODE1_RPC_PORT),$(NODE1))
	$(call run_node,$(NODE2),$(NODE2_PORT),$(NODE2_RPC_PORT))
	@echo "OK! Check logs in privnet/<node_dir>/geth_node.log"
