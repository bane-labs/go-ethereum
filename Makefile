# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: geth android ios evm all test clean privnet_init privnet_nodes_stop privnet_bootnode_stop privnet_stop privnet_clean privnet_start privnet_start_four privnet_start_seven

GETHBIN = ./build/bin
GO ?= latest
GORUN = go run

MAIN_DIR = ./privnet
SINGLE_DIR = $(MAIN_DIR)/single
FOUR_DIR = $(MAIN_DIR)/four
SEVEN_DIR = $(MAIN_DIR)/seven

NODE1 = node1
NODE1_PORT = 30306
NODE1_RPC_PORT = 8552

NODE2 = node2
NODE2_PORT = 30307
NODE2_RPC_PORT = 8553

NODE3 = node3
NODE3_PORT = 30308
NODE3_RPC_PORT = 8554

NODE4 = node4
NODE4_PORT = 30309
NODE4_RPC_PORT = 8555

NODE5 = node5
NODE5_PORT = 30310
NODE5_RPC_PORT = 8556

NODE6 = node6
NODE6_PORT = 30311
NODE6_RPC_PORT = 8557

NODE7 = node7
NODE7_PORT = 30312
NODE7_RPC_PORT = 8558

NODE8 = node8
NODE8_PORT = 30313
NODE8_RPC_PORT = 8559

PASSWORD_LEN = 32
GENESIS_WORK_JSON = genesis_privnet.json

BOOTNODE = bootnode
BOOTNODE_PORT = 30305
BOOTNODE_LOGLEVEL = 5

RESTRICTED_NETWORK = 127.0.0.0/24
NAT_POLICY = none

define generate_bootnode
	@mkdir -p $(1)/$(BOOTNODE)
	@$(GETHBIN)/bootnode -genkey $(1)/$(BOOTNODE)/bootnode.key
	@echo $$($(GETHBIN)/bootnode --writeaddress -nodekey $(1)/$(BOOTNODE)/bootnode.key) > $(1)/$(BOOTNODE)/bootnode_address.txt
endef

define generate_password
   $$(</dev/urandom tr -dc '12345qwertQWERTasdfgASDFGzxcvbZXCVB' | head -c$(PASSWORD_LEN); echo "")
endef

define replace_chainid
	@sed -i "s/_chain_id_/$$(cat $(1)/networkid.txt)/gI" $(1)/$(GENESIS_WORK_JSON)
endef

define replace_node_address
	@echo $$(cat $(1)/$(2)/keystore/* | sed -En 's/.*"address":"([^"]*).*/\1/p') > $(1)/$(2)/node_address.txt
	@sed -i "s/$(2)/$$(cat $(1)/$(2)/node_address.txt)/gI" $(1)/$(GENESIS_WORK_JSON)
endef

define create_account
    @mkdir -p $(1)/$(2)
    @echo $(call generate_password) > $(1)/$(2)/password.txt
    @$(GETHBIN)/geth --datadir $(1)/$(2) account new --password $(1)/$(2)/password.txt
    $(call replace_node_address,$(1),$(2))
    @echo "Account $(1): "$$(cat $(1)/$(2)/node_address.txt)
endef

define run_bootnode
    @$(GETHBIN)/bootnode -nodekey $(1)/$(BOOTNODE)/bootnode.key \
    	-addr :$(BOOTNODE_PORT) \
    	-verbosity $(BOOTNODE_LOGLEVEL) > $(1)/$(BOOTNODE)/bootnode.log 2>&1 &
endef

define run_miner_node
	$(call run_node,$(1),$(2),$(3),$(4),--mine --miner.etherbase="0x$$(cat $(1)/$(5)/node_address.txt)")
endef

define run_node
	@$(GETHBIN)/geth --datadir $(1)/$(2) \
		--port $(3) \
		--bootnodes "enode://$$(cat $(1)/$(BOOTNODE)/bootnode_address.txt)@127.0.0.1:0?discport=$(BOOTNODE_PORT)" \
		--networkid "$$(cat $(1)/networkid.txt)" \
		--unlock 0x"$$(cat $(1)/$(2)/node_address.txt)" \
		--authrpc.port $(4) \
		--password $(1)/$(2)/password.txt \
		--metrics \
		--nat $(NAT_POLICY) \
		--netrestrict $(RESTRICTED_NETWORK) \
		--verbosity 5 \
		$(5) >  $(1)/$(2)/geth_node.log 2>&1 &
endef

define init_node
    @$(GETHBIN)/geth init --datadir $(1)/$(2) $(1)/$(GENESIS_WORK_JSON) > $(1)/$(2)/geth_init.log 2>&1
endef

geth:
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	@echo "Run \"$(GETHBIN)/geth\" to launch geth."

all:
	$(GORUN) build/ci.go install

test: all
	$(GORUN) build/ci.go test

lint: ## Run linters.
	$(GORUN) build/ci.go lint

clean:
	go clean -cache
	rm -fr build/_workspace/pkg/ $(GETHBIN)/*

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
	@find $(SINGLE_DIR)/* -type d -name 'keystore' -exec rm -rf {} +
	@mkdir -p $(SINGLE_DIR)
	@echo "Generate  $(GENESIS_WORK_JSON) file"
	@cp $(SINGLE_DIR)/genesis_template.json $(SINGLE_DIR)/$(GENESIS_WORK_JSON)
	@echo $$(date +'%y%m%d%H%M') > $(SINGLE_DIR)/networkid.txt
	@echo "Network ID is "$$(cat $(SINGLE_DIR)/networkid.txt)
	@echo "Generate bootnode"
	$(call generate_bootnode,$(SINGLE_DIR))
	$(call replace_chainid,$(SINGLE_DIR))
	@echo "Create accounts"
	$(call create_account,$(SINGLE_DIR),$(NODE1))
	$(call create_account,$(SINGLE_DIR),$(NODE2))
	@echo "OK! For starting use 'make privnet_start'"

privnet_init_four: privnet_clean
	@find $(FOUR_DIR)/* -type d -name 'keystore' -exec rm -rf {} +
	@mkdir -p $(FOUR_DIR)
	@echo "Generate  $(GENESIS_WORK_JSON) file"
	@cp $(FOUR_DIR)/genesis_template.json $(FOUR_DIR)/$(GENESIS_WORK_JSON)
	@echo $$(date +'%y%m%d%H%M') > $(FOUR_DIR)/networkid.txt
	@echo "Network ID is "$$(cat $(FOUR_DIR)/networkid.txt)
	@echo "Generate bootnode"
	$(call generate_bootnode,$(FOUR_DIR))
	$(call replace_chainid,$(FOUR_DIR))
	@echo "Create accounts"
	$(call create_account,$(FOUR_DIR),$(NODE1))
	$(call create_account,$(FOUR_DIR),$(NODE2))
	$(call create_account,$(FOUR_DIR),$(NODE3))
	$(call create_account,$(FOUR_DIR),$(NODE4))
	$(call create_account,$(FOUR_DIR),$(NODE5))
	@echo "OK! For starting use 'make privnet_start_four'"

privnet_init_seven: privnet_clean
	@find $(SEVEN_DIR)/* -type d -name 'keystore' -exec rm -rf {} +
	@mkdir -p $(SEVEN_DIR)
	@echo "Generate  $(GENESIS_WORK_JSON) file"
	@cp $(SEVEN_DIR)/genesis_template.json $(SEVEN_DIR)/$(GENESIS_WORK_JSON)
	@echo $$(date +'%y%m%d%H%M') > $(SEVEN_DIR)/networkid.txt
	@echo "Network ID is "$$(cat $(SEVEN_DIR)/networkid.txt)
	@echo "Generate bootnode"
	$(call generate_bootnode,$(SEVEN_DIR))
	$(call replace_chainid,$(SEVEN_DIR))
	@echo "Create accounts"
	$(call create_account,$(SEVEN_DIR),$(NODE1))
	$(call create_account,$(SEVEN_DIR),$(NODE2))
	$(call create_account,$(SEVEN_DIR),$(NODE3))
	$(call create_account,$(SEVEN_DIR),$(NODE4))
	$(call create_account,$(SEVEN_DIR),$(NODE5))
	$(call create_account,$(SEVEN_DIR),$(NODE6))
	$(call create_account,$(SEVEN_DIR),$(NODE7))
	$(call create_account,$(SEVEN_DIR),$(NODE8))
	@echo "OK! For starting use 'make privnet_start_seven'"

privnet_nodes_stop:
	@echo "Killing nodes processes"
	@killall -w -v -INT geth || :

privnet_bootnode_stop:
	@echo "Killing bootnode processes"
	@killall -w -v -9 bootnode || :

privnet_stop: privnet_bootnode_stop privnet_nodes_stop

privnet_clean: privnet_stop
	@echo "Cleaning the nodes database files from $(MAIN_DIR)"
	@find $(MAIN_DIR)/* -type d -name 'geth' -print -exec rm -rf {} +
	@find $(MAIN_DIR)/* -type s,f -not \( -path '*/keystore/*' -or -name '*.json' -or -name '*.txt' -or -name '*.key' -or -name '*.md' \) -print -exec rm -f {} +

$(SINGLE_DIR)/$(NODE1)/geth:
	@echo "Initializing $(NODE1) from genesis"
	$(call init_node,$(SINGLE_DIR),$(NODE1))

$(SINGLE_DIR)/$(NODE2)/geth:
	@echo "Initializing $(NODE2) from genesis"
	$(call init_node,$(SINGLE_DIR),$(NODE2))

$(FOUR_DIR)/$(NODE1)/geth:
	@echo "Initializing $(NODE1) from genesis"
	$(call init_node,$(FOUR_DIR),$(NODE1))

$(FOUR_DIR)/$(NODE2)/geth:
	@echo "Initializing $(NODE2) from genesis"
	$(call init_node,$(FOUR_DIR),$(NODE2))

$(FOUR_DIR)/$(NODE3)/geth:
	@echo "Initializing $(NODE3) from genesis"
	$(call init_node,$(FOUR_DIR),$(NODE3))

$(FOUR_DIR)/$(NODE4)/geth:
	@echo "Initializing $(NODE4) from genesis"
	$(call init_node,$(FOUR_DIR),$(NODE4))

$(FOUR_DIR)/$(NODE5)/geth:
	@echo "Initializing $(NODE5) from genesis"
	$(call init_node,$(FOUR_DIR),$(NODE5))

$(SEVEN_DIR)/$(NODE1)/geth:
	@echo "Initializing $(NODE1) from genesis"
	$(call init_node,$(SEVEN_DIR),$(NODE1))

$(SEVEN_DIR)/$(NODE2)/geth:
	@echo "Initializing $(NODE2) from genesis"
	$(call init_node,$(SEVEN_DIR),$(NODE2))

$(SEVEN_DIR)/$(NODE3)/geth:
	@echo "Initializing $(NODE3) from genesis"
	$(call init_node,$(SEVEN_DIR),$(NODE3))

$(SEVEN_DIR)/$(NODE4)/geth:
	@echo "Initializing $(NODE4) from genesis"
	$(call init_node,$(SEVEN_DIR),$(NODE4))

$(SEVEN_DIR)/$(NODE5)/geth:
	@echo "Initializing $(NODE5) from genesis"
	$(call init_node,$(SEVEN_DIR),$(NODE5))

$(SEVEN_DIR)/$(NODE6)/geth:
	@echo "Initializing $(NODE6) from genesis"
	$(call init_node,$(SEVEN_DIR),$(NODE6))

$(SEVEN_DIR)/$(NODE7)/geth:
	@echo "Initializing $(NODE7) from genesis"
	$(call init_node,$(SEVEN_DIR),$(NODE7))

$(SEVEN_DIR)/$(NODE8)/geth:
	@echo "Initializing $(NODE8) from genesis"
	$(call init_node,$(SEVEN_DIR),$(NODE8))

privnet_start: $(SINGLE_DIR)/$(NODE1)/geth $(SINGLE_DIR)/$(NODE2)/geth
	@echo "Starting nodes..."
	$(call run_bootnode,$(SINGLE_DIR))
	$(call run_miner_node,$(SINGLE_DIR),$(NODE1),$(NODE1_PORT),$(NODE1_RPC_PORT),$(NODE1))
	$(call run_node,$(SINGLE_DIR),$(NODE2),$(NODE2_PORT),$(NODE2_RPC_PORT))
	@echo "OK! Check logs in $(SINGLE_DIR)/<node_dir>/geth_node.log"

privnet_start_four: $(FOUR_DIR)/$(NODE1)/geth $(FOUR_DIR)/$(NODE2)/geth $(FOUR_DIR)/$(NODE3)/geth $(FOUR_DIR)/$(NODE4)/geth $(FOUR_DIR)/$(NODE5)/geth
	@echo "Starting nodes..."
	$(call run_bootnode,$(FOUR_DIR))
	$(call run_miner_node,$(FOUR_DIR),$(NODE1),$(NODE1_PORT),$(NODE1_RPC_PORT),$(NODE1))
	$(call run_miner_node,$(FOUR_DIR),$(NODE2),$(NODE2_PORT),$(NODE2_RPC_PORT),$(NODE2))
	$(call run_miner_node,$(FOUR_DIR),$(NODE3),$(NODE3_PORT),$(NODE3_RPC_PORT),$(NODE3))
	$(call run_miner_node,$(FOUR_DIR),$(NODE4),$(NODE4_PORT),$(NODE4_RPC_PORT),$(NODE4))
	$(call run_node,$(FOUR_DIR),$(NODE5),$(NODE5_PORT),$(NODE5_RPC_PORT))
	@echo "OK! Check logs in $(FOUR_DIR)/<node_dir>/geth_node.log"

privnet_start_seven: $(SEVEN_DIR)/$(NODE1)/geth $(SEVEN_DIR)/$(NODE2)/geth $(SEVEN_DIR)/$(NODE3)/geth $(SEVEN_DIR)/$(NODE4)/geth $(SEVEN_DIR)/$(NODE5)/geth $(SEVEN_DIR)/$(NODE6)/geth $(SEVEN_DIR)/$(NODE7)/geth $(SEVEN_DIR)/$(NODE8)/geth
	@echo "Starting nodes..."
	$(call run_bootnode,$(SEVEN_DIR))
	$(call run_miner_node,$(SEVEN_DIR),$(NODE1),$(NODE1_PORT),$(NODE1_RPC_PORT),$(NODE1))
	$(call run_miner_node,$(SEVEN_DIR),$(NODE2),$(NODE2_PORT),$(NODE2_RPC_PORT),$(NODE2))
	$(call run_miner_node,$(SEVEN_DIR),$(NODE3),$(NODE3_PORT),$(NODE3_RPC_PORT),$(NODE3))
	$(call run_miner_node,$(SEVEN_DIR),$(NODE4),$(NODE4_PORT),$(NODE4_RPC_PORT),$(NODE4))
	$(call run_miner_node,$(SEVEN_DIR),$(NODE5),$(NODE5_PORT),$(NODE5_RPC_PORT),$(NODE5))
	$(call run_miner_node,$(SEVEN_DIR),$(NODE6),$(NODE6_PORT),$(NODE6_RPC_PORT),$(NODE6))
	$(call run_miner_node,$(SEVEN_DIR),$(NODE7),$(NODE7_PORT),$(NODE7_RPC_PORT),$(NODE7))
	$(call run_node,$(SEVEN_DIR),$(NODE8),$(NODE8_PORT),$(NODE8_RPC_PORT))
	@echo "OK! Check logs in $(SEVEN_DIR)/<node_dir>/geth_node.log"