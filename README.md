# escrow-checker

A tool for verifying that escrow accounts, associated with channels in [IBC](https://github.com/cosmos/ibc-go), 
have balances that are equal to the total supply of the assets on a counterparty chain. 

## Build & Run

Clone the repo, navigate to the root directory, compile, and run the binary.

```bash
$ git clone https://github.com/strangelove-ventures/escrow-checker.git
$ cd escrow-checker
$ go build
$ ./escrow-check
```

## Configuration

In `main.go` there are a few variables that can be used to change the program's configuration.
You will need to re-compile the program after changing these values.

- `configPath`: the location of the config file
- `targetChainID`: chain ID of the chain whose escrow accounts you want to validate
- `maxWorkers`: maximum number of goroutines that will run concurrently while querying escrow account info
- `targetChannels`: channel IDs associated with escrow accounts you want to validate, empty slice to check all escrow accounts

Some basic information is required to query each chain for information about the escrow accounts and total supply of tokens.
In `config/config.yaml` you will need to add an entry for your target chain as well as every counterparty chain you want to 
query.

Example:
```yaml
  - name: osmosis
    chain-id: osmosis-1
    account-prefix: osmo
    rpc-address: https://osmosis-rpc.onivalidator.com:443
    timeout: 30s
```