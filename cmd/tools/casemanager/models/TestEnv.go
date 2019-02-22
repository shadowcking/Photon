package models

import (
	"context"
	"crypto/ecdsa"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"fmt"

	"encoding/json"

	"io"

	"strconv"

	"github.com/SmartMeshFoundation/Photon/accounts"
	"github.com/SmartMeshFoundation/Photon/network/rpc/contracts"
	"github.com/SmartMeshFoundation/Photon/network/rpc/contracts/test/tokens/smttoken"
	"github.com/SmartMeshFoundation/Photon/network/rpc/contracts/test/tokens/tokenerc223approve"
	"github.com/SmartMeshFoundation/Photon/utils"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/huamou/config"
)

// TestEnv env manager for test
type TestEnv struct {
	Conn                *ethclient.Client
	CaseName            string
	Main                string
	DataDir             string
	KeystorePath        string
	PasswordFile        string
	XMPPServer          string
	EthRPCEndpoint      string
	TokenNetwork        *contracts.TokensNetwork
	TokenNetworkAddress string
	UseMatrix           bool
	Verbosity           int
	Debug               bool
	Nodes               []*PhotonNode
	Tokens              []*Token
	Channels            []*Channel
	Keys                []*ecdsa.PrivateKey `json:"-"`
	UseOldToken         bool
}

// Logger : global case logger
var Logger *log.Logger
var globalPassword = "123"

type logTee struct {
	w1 io.Writer
	w2 io.Writer
}

func (t *logTee) Write(p []byte) (n int, err error) {
	n, err = t.w1.Write(p)
	_, err = t.w2.Write(p)
	if err != nil {
		panic(err)
	}
	return
}

// NewTestEnv default contractor
func NewTestEnv(configFilePath string, useMatrix bool, ethEndPoint string) (env *TestEnv, err error) {
	bind.ReInitNonceMap()
	c, err := config.ReadDefault(configFilePath)
	if err != nil {
		log.Println("Load config error:", err)
		return
	}
	env = new(TestEnv)
	env.CaseName = c.RdString("COMMON", "case_name", "DefaultName")
	// init logger
	logfile := "./log/" + env.CaseName + ".log"
	logFile, err := os.Create(logfile)
	if err != nil {
		log.Fatalln("Create log file error !", logfile)
	}
	Logger = log.New(&logTee{logFile, os.Stderr}, "", log.LstdFlags|log.Lshortfile)
	Logger.Println("Start to prepare env for " + env.CaseName + "...")
	env.UseMatrix = useMatrix
	env.Main = c.RdString("COMMON", "main", "photon")
	env.DataDir = c.RdString("COMMON", "data_dir", ".photon")
	env.KeystorePath = c.RdString("COMMON", "keystore_path", "../../../testdata/casemanager-keystore")
	env.PasswordFile = c.RdString("COMMON", "password_file", "../../../testdata/casemanager-keystore/pass")
	env.XMPPServer = c.RdString("COMMON", "xmpp-server", "")
	env.EthRPCEndpoint = ethEndPoint
	env.Verbosity = c.RdInt("COMMON", "verbosity", 5)
	env.Debug = c.RdBool("COMMON", "debug", true)
	env.UseOldToken = false
	// Create an IPC based RPC connection to a remote node and an authorized transactor
	conn, err := ethclient.Dial(env.EthRPCEndpoint)
	if err != nil {
		Logger.Fatalf(fmt.Sprintf("Failed to connect to the Ethereum client: %v", err))
	}
	env.Conn = conn
	_, key := promptAccount(env.KeystorePath)
	tokenNetworkAddress, tokenNetwork := loadTokenNetworkContract(c, conn, key)
	env.TokenNetwork = tokenNetwork
	env.TokenNetworkAddress = tokenNetworkAddress.String()
	env.Nodes = loadNodes(c)
	env.Tokens = loadTokenAddrs(c, env, conn, key)
	env.Channels = loadAndBuildChannels(c, env, conn)
	env.KillAllPhotonNodes()
	env.ClearHistoryData()
	env.Println(env.CaseName + " env:")
	Logger.Println("Env Prepare SUCCESS")
	return
}

func loadTokenNetworkContract(c *config.Config, conn *ethclient.Client, key *ecdsa.PrivateKey) (tokenNetworkAddress common.Address, tokenNetwork *contracts.TokensNetwork) {
	addr := c.RdString("COMMON", "token_network_address", "new")
	if addr == "new" {
		tokenNetworkAddress, tokenNetwork = deployTokenNetworkContract(conn, key)
	} else {
		var err error
		tokenNetworkAddress = common.HexToAddress(addr)
		tokenNetwork, err = contracts.NewTokensNetwork(tokenNetworkAddress, conn)
		if err != nil {
			panic(err)
		}
	}
	Logger.Println("Load RegistryAddress SUCCESS")
	return
}
func deployTokenNetworkContract(conn *ethclient.Client, key *ecdsa.PrivateKey) (tokenNetworkAddress common.Address, tokenNetwork *contracts.TokensNetwork) {
	auth := bind.NewKeyedTransactor(key)
	var tx *types.Transaction
	chainID, err := conn.NetworkID(context.Background())
	if err != nil {
		log.Fatalf("failed to get network id %s", err)
	}
	tokenNetworkAddress, tx, tokenNetwork, err = contracts.DeployTokensNetwork(auth, conn, chainID)
	if err != nil {
		log.Fatalf("failed to deploy TokenNetworkRegistry %s", err)
	}
	ctx := context.Background()
	_, err = bind.WaitDeployed(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to deploy contact when mining :%v", err)
	}
	fmt.Printf("deploy TokenNetwork complete... TokenNetworkAddress=%s\n", tokenNetworkAddress.String())
	return
}
func promptAccount(keystorePath string) (addr common.Address, key *ecdsa.PrivateKey) {
	am := accounts.NewAccountManager(keystorePath)
	if len(am.Accounts) == 0 {
		log.Fatal(fmt.Sprintf("No Ethereum accounts found in the directory %s", keystorePath))
		os.Exit(1)
	}
	addr = am.Accounts[0].Address
	for i := 0; i < 3; i++ {
		//retries three times
		if len(globalPassword) <= 0 {
			fmt.Printf("Enter the password to unlock")
			_, err := fmt.Scanln(&globalPassword)
			if err != nil {
				log.Fatal(err)
			}
		}
		keyBin, err := am.GetPrivateKey(addr, globalPassword)
		if err != nil && i == 3 {
			log.Fatal(fmt.Sprintf("Exhausted passphrase unlock attempts for %s. Aborting ...", addr))
			os.Exit(1)
		}
		if err != nil {
			log.Println(fmt.Sprintf("password incorrect\n Please try again or kill the process to quit.\nUsually Ctrl-c."))
			continue
		}
		key, err = crypto.ToECDSA(keyBin)
		if err != nil {
			log.Println(fmt.Sprintf("private key to bytes err %s", err))
		}
		break
	}
	return
}
func loadNodes(c *config.Config) (nodes []*PhotonNode) {
	options, err := c.Options("NODE")
	if err != nil {
		panic(err)
	}
	sort.Strings(options)
	for _, option := range options {
		s := strings.Split(c.RdString("NODE", option, ""), ",")
		nodes = append(nodes, &PhotonNode{
			Name:          option,
			Host:          "http://" + s[1],
			Address:       s[0],
			APIAddress:    s[1],
			ListenAddress: s[1] + "0",
			DebugCrash:    false,
		})
	}
	Logger.Println("Load Nodes SUCCESS")
	return
}

func loadTokenAddrs(c *config.Config, env *TestEnv, conn *ethclient.Client, key *ecdsa.PrivateKey) (tokens []*Token) {
	options, err := c.Options("TOKEN")
	if err != nil {
		panic(err)
	}
	sort.Strings(options)
	for _, option := range options {
		addr := c.RdString("TOKEN", option, "")
		if addr == "new" {
			token, tokenAddress := deployNewToken(env, conn, key)
			Logger.Printf("New Token =%s\n", tokenAddress.String())
			tokens = append(tokens, &Token{
				Name:         option,
				Token:        token,
				TokenAddress: tokenAddress,
			})
		} else if addr == "smttoken" {
			token, tokenAddress := deploySMTToken(env, conn, key)
			Logger.Printf("New SMTToken =%s\n", tokenAddress.String())
			tokens = append(tokens, &Token{
				Name:         option,
				Token:        token,
				TokenAddress: tokenAddress,
			})
		} else {
			env.UseOldToken = true
			tokenAddress := common.HexToAddress(addr)
			token, err := contracts.NewToken(tokenAddress, conn)
			if err != nil {
				panic(err)
			}
			tokens = append(tokens, &Token{
				Name:         option,
				Token:        token,
				TokenAddress: tokenAddress,
			})
		}
	}
	Logger.Println("Load Tokens SUCCESS")
	return
}
func deploySMTToken(env *TestEnv, conn *ethclient.Client, key *ecdsa.PrivateKey) (token *contracts.Token, tokenAddress common.Address) {
	var err error
	auth := bind.NewKeyedTransactor(key)
	auth.Value = big.NewInt(1)
	tokenAddress, tx, _, err := smttoken.DeploySMTToken(auth, conn, "", common.HexToAddress(env.TokenNetworkAddress))
	if err != nil {
		log.Fatalf("Failed to DeploySMTToken: %v", err)
	}
	fmt.Printf("SMTToken deploy tx=%s\n", tx.Hash().String())
	ctx := context.Background()
	_, err = bind.WaitDeployed(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to deploy contact when mining :%v", err)
	}
	fmt.Printf("DeploySMTToken complete... tokenAddress=%s\n", tokenAddress.String())

	token, err = contracts.NewToken(tokenAddress, conn)
	if err != nil {
		panic(fmt.Sprintf("err for newtoken err %s", err))
	}
	return
}
func deployNewToken(env *TestEnv, conn *ethclient.Client, key *ecdsa.PrivateKey) (token *contracts.Token, tokenAddress common.Address) {
	var err error
	tokenAddress = newToken(key, conn)
	token, err = contracts.NewToken(tokenAddress, conn)
	if err != nil {
		panic(fmt.Sprintf("err for newtoken err %s", err))
	}
	am := accounts.NewAccountManager(env.KeystorePath)
	var accounts []common.Address
	for _, node := range env.Nodes {
		address := common.HexToAddress(node.Address)
		accounts = append(accounts, address)
		keyBin, err := am.GetPrivateKey(address, globalPassword)
		if err != nil {
			Logger.Fatalf("password error for %s", address.String())
		}
		keyTemp, err := crypto.ToECDSA(keyBin)
		if err != nil {
			Logger.Fatalf("ToECDSA err %s", err)
		}
		env.Keys = append(env.Keys, keyTemp)
	}
	transferMoneyForAccounts(key, conn, accounts, token)
	return
}
func newToken(key *ecdsa.PrivateKey, conn *ethclient.Client) (tokenAddr common.Address) {
	auth := bind.NewKeyedTransactor(key)
	tokenAddr, tx, _, err := tokenerc223approve.DeployHumanERC223Token(auth, conn, big.NewInt(500000000), "test symoble", 0)
	if err != nil {
		log.Fatalf("Failed to DeployHumanStandardToken: %v", err)
	}
	fmt.Printf("token deploy tx=%s\n", tx.Hash().String())
	ctx := context.Background()
	_, err = bind.WaitDeployed(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to deploy contact when mining :%v", err)
	}
	fmt.Printf("DeployHumanStandardToken complete... tokenAddress=%s\n", tokenAddr.String())
	return
}

// TransferMoneyForAccounts :
func transferMoneyForAccounts(key *ecdsa.PrivateKey, conn *ethclient.Client, accounts []common.Address, token *contracts.Token) {
	wg := sync.WaitGroup{}
	wg.Add(len(accounts))
	//auth := bind.NewKeyedTransactor(key)
	//nonce, err := conn.PendingNonceAt(context.Background(), auth.From)
	//if err != nil {
	//	panic(err)
	//}
	for index, account := range accounts {
		go func(account common.Address, i int) {
			auth2 := bind.NewKeyedTransactor(key)
			//auth2.Nonce = big.NewInt(int64(nonce) + int64(i))
			tx, err := token.Transfer(auth2, account, big.NewInt(5000000), nil)
			if tx == nil {
				panic("transfer should use approve and transfer from instead")
			}
			if err != nil {
				Logger.Fatalf("Failed to Transfer: %v", err)
			}
			ctx := context.Background()
			_, err = bind.WaitMined(ctx, conn, tx)
			if err != nil {
				Logger.Fatalf("failed to Transfer when mining :%v", err)
			}
			wg.Done()
		}(account, index)
	}
	wg.Wait()
	for _, account := range accounts {
		b, err := token.BalanceOf(nil, account)
		if err != nil {
			panic(err)
		}
		fmt.Printf("account %s has token %s\n", utils.APex(account), b)
	}
}
func loadAndBuildChannels(c *config.Config, env *TestEnv, conn *ethclient.Client) (channels []*Channel) {
	options, err := c.Options("CHANNEL")
	if err != nil {
		panic(err)
	}
	if options == nil || len(options) == 0 {
		return
	}
	wg := sync.WaitGroup{}
	wg.Add(len(options))
	for _, o := range options {
		func(option string) {
			defer wg.Done()
			s := strings.Split(c.RdString("CHANNEL", option, ""), ",")
			_, token := env.GetTokenByName(s[2])
			if env.UseOldToken {
				fmt.Println("use old token , do not create channel...")
				return
			}
			index1, account1 := env.GetNodeAddressByName(s[0])
			key1 := env.Keys[index1]
			amount1, err := strconv.ParseInt(s[3], 10, 64)
			index2, account2 := env.GetNodeAddressByName(s[1])
			key2 := env.Keys[index2]
			amount2, err := strconv.ParseInt(s[4], 10, 64)
			settledTimeout, err := strconv.ParseUint(s[5], 10, 64)
			if err != nil {
				panic(err)
			}
			creatAChannelAndDeposit(env, account1, account2, key1, key2, big.NewInt(amount1), big.NewInt(amount2), settledTimeout, token, conn)
		}(o)
	}
	wg.Wait()
	Logger.Println("Load and create channels SUCCESS")
	return nil
}

func creatAChannelAndDeposit(env *TestEnv, account1, account2 common.Address, key1, key2 *ecdsa.PrivateKey, amount1 *big.Int, amount2 *big.Int, settledTimeout uint64, token *Token, conn *ethclient.Client) {
	log.Printf("createchannel between %s-%s,token=%s\n", utils.APex(account1), utils.APex(account2), utils.APex(token.TokenAddress))
	var tx *types.Transaction
	var err error
	auth1 := bind.NewKeyedTransactor(key1)
	auth2 := bind.NewKeyedTransactor(key2)
	if amount1.Int64() > 0 {
		approveAccountIfNeeded(token, auth1, common.HexToAddress(env.TokenNetworkAddress), amount1, conn)
		tx, err = env.TokenNetwork.Deposit(auth1, token.TokenAddress, account1, account2, amount1, settledTimeout)
		if err != nil {
			panic(err)
		}
		_, err = bind.WaitMined(context.Background(), conn, tx)
		if err != nil {
			panic(err)
		}
	}
	if amount2.Int64() > 0 {
		approveAccountIfNeeded(token, auth2, common.HexToAddress(env.TokenNetworkAddress), amount2, conn)
		tx, err = env.TokenNetwork.Deposit(auth2, token.TokenAddress, account2, account1, amount2, settledTimeout)
		if err != nil {
			panic(err)
		}
		_, err = bind.WaitMined(context.Background(), conn, tx)
		if err != nil {
			panic(err)
		}
	}
}

func approveAccount(token *contracts.Token, auth *bind.TransactOpts, tokenNetworkAddress common.Address, amount *big.Int, conn *ethclient.Client) {
	approveAmt := new(big.Int)
	approveAmt = approveAmt.Mul(amount, big.NewInt(100)) //保证多个通道创建的时候不会因为approve冲突
	tx, err := token.Approve(auth, tokenNetworkAddress, approveAmt)
	if err != nil {
		log.Fatalf("Failed to Approve: %v", err)
	}
	ctx := context.Background()
	_, err = bind.WaitMined(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to Approve when mining :%v", err)
	}
	log.Printf("approve account %s %d tokens to %s success\n", utils.APex(auth.From), approveAmt, utils.APex(tokenNetworkAddress))
}

var approveMap = make(map[common.Hash]int64)
var approveMapLock = sync.Mutex{}

func approveAccountIfNeeded(token *Token, auth *bind.TransactOpts, tokenNetworkAddress common.Address, amount *big.Int, conn *ethclient.Client) {
	key := utils.Sha3(tokenNetworkAddress[:], auth.From[:], token.TokenAddress[:])
	m, ok := approveMap[key]
	if ok && m > amount.Int64() {
		return
	}
	approveMapLock.Lock()
	defer approveMapLock.Unlock()
	approveAccount(token.Token, auth, tokenNetworkAddress, amount, conn)
	approveAmt := new(big.Int)
	approveAmt = approveAmt.Mul(amount, big.NewInt(100))
	approveMap[key] = approveAmt.Int64()
}

// KillAllPhotonNodes kill all photon node
func (env *TestEnv) KillAllPhotonNodes() {
	var pstr2 []string
	//kill the old process
	if runtime.GOOS == "windows" {
		pstr2 = append(pstr2, "-F")
		pstr2 = append(pstr2, "-IM")
		pstr2 = append(pstr2, "photon*")
		ExecShell("taskkill", pstr2, "./log/killall.log", true)
	} else {
		pstr2 = append(pstr2, "-9")
		pstr2 = append(pstr2, "photon")
		ExecShell("killall", pstr2, "./log/killall.log", true)
	}
	Logger.Println("Kill all photon nodes SUCCESS")
}

// ClearHistoryData :
func (env *TestEnv) ClearHistoryData() {
	if env.DataDir == "" {
		return
	}
	err := filepath.Walk(env.DataDir, func(path string, fi os.FileInfo, err error) error {
		if nil == fi {
			return err
		}
		if !fi.IsDir() {
			return nil
		}
		name := fi.Name()

		if name == ".photon" {
			err := os.RemoveAll(path)
			if err != nil {
				fmt.Println("delet dir error:", err)
			}
		}
		Logger.Println("Clear history data SUCCESS ")
		return nil
	})
	if err != nil {
		Logger.Println("No history data ")
	}
}

// GetTokenByName :
func (env *TestEnv) GetTokenByName(tokenName string) (index int, token *Token) {
	for index, token := range env.Tokens {
		if token.Name == tokenName {
			return index, token
		}
	}
	return
}

// GetNodeAddressByName :
func (env *TestEnv) GetNodeAddressByName(nodeName string) (index int, address common.Address) {
	for index, node := range env.Nodes {
		if node.Name == nodeName {
			return index, common.HexToAddress(node.Address)
		}
	}
	return
}

// GetNodeByAddress :
func (env *TestEnv) GetNodeByAddress(nodeAddress string) *PhotonNode {
	for _, node := range env.Nodes {
		if node.Address == nodeAddress {
			return node
		}
	}
	return nil
}

//Println print all
func (env *TestEnv) Println(header string) {
	Logger.Println(header)
	buf, err := json.MarshalIndent(env, "", "\t")
	if err != nil {
		panic(err)
	}
	Logger.Println(string(buf))
}
