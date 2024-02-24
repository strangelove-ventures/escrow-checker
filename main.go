package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	sdktypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	tendermint "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
	"github.com/ghodss/yaml"
)

const (
	configPath  = "./config/config.yaml"
	pathPattern = `transfer/channel-\d+`
)

type AssetInfo struct {
	Denom               transfertypes.DenomTrace
	PortID              string
	ChannelID           string
	CounterpartyChainID string
	EscrowAddress       string
	EscrowBalance       sdktypes.Coin
	CounterpartyBalance sdktypes.Coin
}

func main() {
	cfg, err := readConfig(configPath)
	if err != nil {
		panic(err)
	}

	clients, err := clientsFromConfig(cfg)
	if err != nil {
		panic(err)
	}

	c := clients[1]

	ctx := context.Background()
	res, err := c.QueryDenomTraces(ctx, 0, 1000, 0)
	if err != nil {
		panic(err)
	}

	re := regexp.MustCompile(pathPattern)

	// Get all denom traces & filter to end up with only multi-wrapped IBC assets.
	var assetInfo []*AssetInfo

	for _, denom := range res {
		if isMultiHopAsset(re, denom.Path) {
			assetInfo = append(assetInfo, &AssetInfo{
				Denom: denom,
			})
		}
	}

	// Get port and channel IDs on this chain, so we can query for the escrow account address later.
	// Query the client state for this particular channel, so we can retrieve the counterparty chain ID.
	for _, info := range assetInfo {
		info.PortID, info.ChannelID = getPortAndChannelIDs(re, info.Denom.Path)

		res, err := c.QueryChannelClientState(info.PortID, info.ChannelID)
		if err != nil {
			panic(err)
		}

		cs := &tendermint.ClientState{}
		err = proto.Unmarshal(res.IdentifiedClientState.ClientState.Value, cs)
		if err != nil {
			panic(err)
		}

		// TODO: debug output remove
		fmt.Printf("Counterparty chain ID: %s \n", cs.ChainId)

		info.CounterpartyChainID = cs.ChainId
	}

	// Query for the escrow account address and its balance for the relevant IBC denoms.
	for _, info := range assetInfo {
		addr, err := c.QueryEscrowAddress(ctx, info.PortID, info.ChannelID)
		if err != nil {
			panic(err)
		}

		// TODO: debug output remove
		fmt.Printf("Escrow Addr: %s \n", addr)

		info.EscrowAddress = addr

		bal, err := c.QueryBalance(ctx, addr, info.Denom.IBCDenom())
		if err != nil {
			panic(err)
		}

		// TODO: debug output remove
		fmt.Printf("Token %s amount: %s \n", info.Denom.String(), bal)

		info.EscrowBalance = bal
	}

	// Query the counterparty chain to get the total supply of the IBC denom asset.
	for _, info := range assetInfo {
		client, err := clients.clientByChainID(info.CounterpartyChainID)
		if err != nil {
			fmt.Println(err)
			continue
		}

		// Find the index of the first match
		firstMatchIndex := re.FindStringIndex(info.Denom.Path)

		// Remove the first match from the input string
		path := info.Denom.Path[:firstMatchIndex[0]] + info.Denom.Path[firstMatchIndex[1]+1:]

		denom := transfertypes.ParseDenomTrace(fmt.Sprintf("%s/%s", path, info.Denom.BaseDenom))
		amount, err := client.QueryBankTotalSupply(ctx, denom.IBCDenom())
		if err != nil {
			panic(err)
		}

		// TODO: debug output remove
		fmt.Printf("Total supply of %s on chain %s: %s \n", info.Denom.IBCDenom(), info.CounterpartyChainID, amount)

		info.CounterpartyBalance = amount
	}

	// Assert that the escrow account balance is equal to the total supply on the counterparty.
	for _, info := range assetInfo {
		if !info.EscrowBalance.Equal(info.CounterpartyBalance) {
			fmt.Println("--------------------------------------------")
			fmt.Println("Discrepancy found!")
			fmt.Printf("Counterparty Chain ID: %s \n", info.CounterpartyChainID)
			fmt.Printf("Escrow Account Address: %s \n", info.EscrowAddress)
			fmt.Printf("Asset IBC Denom: %s \n", info.Denom.IBCDenom())
			fmt.Printf("Escrow Balance: %s \n", info.EscrowBalance)
			fmt.Printf("Counterparty Total Supply: %s \n", info.CounterpartyBalance)
		}
	}
}

func readConfig(path string) (*Config, error) {
	cfgFile, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}

	err = yaml.Unmarshal(cfgFile, cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func clientsFromConfig(cfg *Config) (Clients, error) {
	clients := make([]*Client, len(cfg.Chains))

	for i, c := range cfg.Chains {
		t, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return nil, err
		}

		clients[i] = NewClient(c.ChainID, c.RPCAddress, c.AccountPrefix, t)
	}

	return clients, nil
}

func isMultiHopAsset(regex *regexp.Regexp, path string) bool {
	hops := regex.FindAllString(path, -1)

	if len(hops) > 1 {
		return true
	}

	return false
}

func getPortAndChannelIDs(regex *regexp.Regexp, path string) (portID string, channelID string) {
	matches := regex.FindAllStringIndex(path, -1)

	// Get the index of the last occurrence
	lastMatchIndex := matches[0]

	// Get the substring corresponding to the last occurrence
	lastMatchString := path[lastMatchIndex[0]:lastMatchIndex[1]]

	// TODO: debug output remove
	fmt.Printf("Entire path: %s \n", path)
	fmt.Printf("Last Hop: %+v \n", lastMatchString)

	parts := strings.Split(lastMatchString, "/")

	return parts[0], parts[1]
}
