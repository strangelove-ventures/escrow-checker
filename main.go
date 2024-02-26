package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	sdktypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	tendermint "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
	"github.com/ghodss/yaml"
)

const (
	configPath    = "./config/config.yaml"
	targetChainID = "osmosis-1"
)

var (
	// targetChannels should be an empty slice if you want to run the escrow checker against every escrow account.
	// Otherwise, add the channel-ids associated with escrow accounts you want to target.
	targetChannels = []string{"channel-782", "channel-783"}
)

type Info struct {
	Channel             *chantypes.IdentifiedChannel
	EscrowAddress       string
	Balances            sdktypes.Coins
	CounterpartyChainID string
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

	c, err := clients.clientByChainID(targetChainID)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	channels, err := queryChannels(ctx, c)
	if err != nil {
		panic(err)
	}

	// For every channel query the associated escrow account address, the escrow account balances,
	// and the channel's associated client state in order to identify the counterparty chain.
	infos := make([]*Info, len(channels))
	for i, channel := range channels {
		addr, err := c.QueryEscrowAddress(ctx, channel.PortId, channel.ChannelId)
		if err != nil {
			panic(err)
		}

		bals, err := c.QueryBalances(ctx, addr)
		if err != nil {
			panic(err)
		}

		res, err := c.QueryChannelClientState(channel.PortId, channel.ChannelId)
		if err != nil {
			panic(err)
		}

		cs := &tendermint.ClientState{}
		err = proto.Unmarshal(res.IdentifiedClientState.ClientState.Value, cs)
		if err != nil {
			panic(err)
		}

		// TODO: debug output that can be removed
		//fmt.Printf("Escrow Address: %s \n", addr)
		//for _, bal := range bals.Balances {
		//	fmt.Printf("Balance: %s \n", bal)
		//}

		infos[i] = &Info{
			Channel:             channel,
			EscrowAddress:       addr,
			Balances:            bals.Balances,
			CounterpartyChainID: cs.ChainId,
		}
	}

	// For each token balance in the escrow accounts, query the IBC denom trace from the hash,
	// then compose the denom on the counterparty chain and query the tokens total supply.
	// Assert that the balance in the escrow account is equal to the total supply on the counterparty.
	for _, info := range infos {
		client, err := clients.clientByChainID(info.CounterpartyChainID)
		if err != nil {
			fmt.Println(err)
			continue
		}

		for _, bal := range info.Balances {
			var hash string

			if strings.Contains(bal.Denom, "ibc/") {
				parts := strings.Split(bal.Denom, "/")
				hash = parts[1]
			} else {
				// Found a native denom or non-IBC denom.
				continue
			}

			denom, err := c.QueryDenomTrace(ctx, hash)
			if err != nil {
				panic(err)
			}

			// TODO: debug output that can be removed
			//fmt.Printf("Denom Trace: %s \n", denom)

			path := fmt.Sprintf("%s/%s/%s", info.Channel.Counterparty.PortId, info.Channel.Counterparty.ChannelId, denom.Path)
			counterpartyDenom := transfertypes.ParseDenomTrace(fmt.Sprintf("%s/%s", path, denom.BaseDenom))

			amount, err := client.QueryBankTotalSupply(ctx, counterpartyDenom.IBCDenom())
			if err != nil {
				panic(err)
			}

			// TODO: debug output that can be removed
			//fmt.Printf("Escrow account balance: %s \n", bal.Amount)
			//fmt.Printf("Counterparty Total Supply: %s \n", amount.Amount)

			if !bal.Amount.Equal(amount.Amount) {
				fmt.Println("--------------------------------------------")
				fmt.Println("Discrepancy found!")
				fmt.Printf("Counterparty Chain ID: %s \n", info.CounterpartyChainID)
				fmt.Printf("Escrow Account Address: %s \n", info.EscrowAddress)
				fmt.Printf("Asset Base Denom: %s \n", denom.BaseDenom)
				fmt.Printf("Asset IBC Denom: %s \n", bal.Denom)
				fmt.Printf("Escrow Balance: %s \n", bal.Amount)
				fmt.Printf("Counterparty Total Supply: %s \n", amount)
			}
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

func queryChannels(ctx context.Context, c *Client) ([]*chantypes.IdentifiedChannel, error) {
	var (
		channels []*chantypes.IdentifiedChannel
		err      error
	)

	if len(targetChannels) == 0 {
		channels, err = c.QueryChannels(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		for _, id := range targetChannels {
			channel, err := c.QueryChannel(ctx, id)
			if err != nil {
				return nil, err
			}

			channels = append(channels, channel)
		}
	}

	return channels, nil
}
