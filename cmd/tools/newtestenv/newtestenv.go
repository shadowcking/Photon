package main

import (
	"log"
	"strings"

	"context"

	"fmt"

	"os"

	"crypto/ecdsa"

	"math/big"

	"sync"

	"time"

	"github.com/SmartMeshFoundation/SmartRaiden"
	"github.com/SmartMeshFoundation/SmartRaiden/cmd/tools/newtestenv/createchannel"
	"github.com/SmartMeshFoundation/SmartRaiden/network/rpc/contracts"
	"github.com/SmartMeshFoundation/SmartRaiden/utils"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/slonzok/getpass"
	"gopkg.in/urfave/cli.v1"
)

var globalPassword = "123"

func main() {
	log.SetFlags(log.Lshortfile | log.LstdFlags)
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "address",
			Usage: "The ethereum address you would like raiden to use and for which a keystore file exists in your local system.",
		},
		cli.StringFlag{
			Name:  "keystore-path",
			Usage: "If you have a non-standard path for the ethereum keystore directory provide it using this argument. ",
			//Value: ethutils.DirectoryString{params.DefaultKeyStoreDir()},
			Value: "../../../testdata/keystore",
		},
		cli.StringFlag{
			Name: "eth-rpc-endpoint",
			Usage: `"host:port" address of ethereum JSON-RPC server.\n'
	           'Also accepts a protocol prefix (ws:// or ipc channel) with optional port',`,
			Value: fmt.Sprintf("http://127.0.0.1:8545"), //, node.DefaultWSEndpoint()),
		},
		cli.BoolFlag{
			Name:  "not-create-channel",
			Usage: "not-create channels between node for test.",
		},
	}
	app.Action = mainctx
	app.Name = "newraidenenv"
	app.Version = "0.1"
	err := app.Run(os.Args)
	if err != nil {
		log.Printf("run err %s\n", err)
	}
}

func mainctx(ctx *cli.Context) error {
	fmt.Printf("eth-rpc-endpoint:%s\n", ctx.String("eth-rpc-endpoint"))
	fmt.Printf("not-create-channel=%v\n", ctx.Bool("not-create-channel"))
	// Create an IPC based RPC connection to a remote node and an authorized transactor
	conn, err := ethclient.Dial(ctx.String("eth-rpc-endpoint"))
	if err != nil {
		log.Fatalf(fmt.Sprintf("Failed to connect to the Ethereum client: %v", err))
	}

	_, key := promptAccount(ctx.String("keystore-path"))
	fmt.Println("start to deploy ...")
	registryAddress := deployContract(key, conn)
	//registryAddress := common.HexToAddress("0x7CCBe22b9A5edCc87163EF3014277F027d542D39")
	registry, err := contracts.NewRegistry(registryAddress, conn)
	if err != nil {
		return err
	}
	createTokenAndChannels(key, conn, registry, ctx.String("keystore-path"), !ctx.Bool("not-create-channel"))
	createTokenAndChannels(key, conn, registry, ctx.String("keystore-path"), !ctx.Bool("not-create-channel"))
	return nil
}
func promptAccount(keystorePath string) (addr common.Address, key *ecdsa.PrivateKey) {
	am := smartraiden.NewAccountManager(keystorePath)
	if len(am.Accounts) == 0 {
		log.Fatal(fmt.Sprintf("No Ethereum accounts found in the directory %s", keystorePath))
		os.Exit(1)
	}
	addr = am.Accounts[0].Address
	log.Printf("deploy account = %s", addr.String())
	for i := 0; i < 3; i++ {
		//retries three times
		if len(globalPassword) <= 0 {
			globalPassword = getpass.Prompt("Enter the password to unlock")
		}
		//fmt.Printf("\npassword is %s\n", password)
		keybin, err := am.GetPrivateKey(addr, globalPassword)
		if err != nil && i == 3 {
			log.Fatal(fmt.Sprintf("Exhausted passphrase unlock attempts for %s. Aborting ...", addr))
			os.Exit(1)
		}
		if err != nil {
			log.Println(fmt.Sprintf("password incorrect\n Please try again or kill the process to quit.\nUsually Ctrl-c."))
			continue
		}
		key, err = crypto.ToECDSA(keybin)
		if err != nil {
			log.Println(fmt.Sprintf("private key to bytes err %s", err))
		}
		break
	}
	return
}
func deployContract(key *ecdsa.PrivateKey, conn *ethclient.Client) (RegistryAddress common.Address) {
	auth := bind.NewKeyedTransactor(key)
	//DeployNettingChannelLibrary
	NettingChannelLibraryAddress, tx, _, err := contracts.DeployNettingChannelLibrary(auth, conn)
	if err != nil {
		log.Fatalf("Failed to DeployNettingChannelLibrary: %v", err)
	}
	ctx := context.Background()
	_, err = bind.WaitDeployed(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to deploy contact when mining :%v", err)
	}
	fmt.Printf("DeployNettingChannelLibrary complete...\n")
	//DeployChannelManagerLibrary link nettingchannle library before deploy
	contracts.ChannelManagerLibraryBin = strings.Replace(contracts.ChannelManagerLibraryBin, "__NettingChannelLibrary.sol:NettingCha__", NettingChannelLibraryAddress.String()[2:], -1)
	ChannelManagerLibraryAddress, tx, _, err := contracts.DeployChannelManagerLibrary(auth, conn)
	if err != nil {
		log.Fatalf("Failed to deploy new token contract: %v", err)
	}
	ctx = context.Background()
	_, err = bind.WaitDeployed(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to deploy contact when mining :%v", err)
	}
	fmt.Printf("DeployChannelManagerLibrary complete...\n")
	//DeployRegistry link channelmanagerlibrary before deploy
	contracts.RegistryBin = strings.Replace(contracts.RegistryBin, "__ChannelManagerLibrary.sol:ChannelMan__", ChannelManagerLibraryAddress.String()[2:], -1)
	RegistryAddress, tx, _, err = contracts.DeployRegistry(auth, conn)
	if err != nil {
		log.Fatalf("Failed to deploy new token contract: %v", err)
	}
	ctx = context.Background()
	_, err = bind.WaitDeployed(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to deploy contact when mining :%v", err)
	}
	fmt.Printf("DeployRegistry complete...\n")
	EndpointRegistryAddress, tx, _, err := contracts.DeployEndpointRegistry(auth, conn)
	if err != nil {
		log.Fatalf("Failed to deploy new token contract: %v", err)
	}
	ctx = context.Background()
	_, err = bind.WaitDeployed(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to deploy contact when mining :%v", err)
	}
	fmt.Printf("RegistryAddress=%s\nEndpointRegistryAddress=%s\n", RegistryAddress.String(), EndpointRegistryAddress.String())
	return
}
func createTokenAndChannels(key *ecdsa.PrivateKey, conn *ethclient.Client, registry *contracts.Registry, keystorepath string, createchannel bool) {
	managerAddress, tokenAddress := newToken(key, conn, registry)
	//tokenAddress := common.HexToAddress("0xD29A9Cbf2Ca88981D0794ce94e68495c4bC16F28")
	//managerAddress, _ := registry.ChannelManagerByToken(nil, tokenAddress)
	manager, err := contracts.NewChannelManagerContract(managerAddress, conn)
	if err != nil {
		log.Fatalf("err for NewChannelManagerContract %s", err)
		return
	}
	token, err := contracts.NewToken(tokenAddress, conn)
	if err != nil {
		log.Fatalf("err for newtoken err %s", err)
		return
	}
	am := smartraiden.NewAccountManager(keystorepath)
	var accounts []common.Address
	var keys []*ecdsa.PrivateKey
	for _, account := range am.Accounts {
		accounts = append(accounts, account.Address)
		keybin, err := am.GetPrivateKey(account.Address, globalPassword)
		if err != nil {
			log.Fatalf("password error for %s,err=%s", utils.APex2(account.Address), err)
		}
		keytemp, err := crypto.ToECDSA(keybin)
		if err != nil {
			log.Fatalf("toecdsa err %s", err)
		}
		keys = append(keys, keytemp)
	}
	//fmt.Printf("key=%s\n", key)
	transferMoneyForAccounts(key, conn, accounts[1:], token)
	if createchannel {
		createChannels(conn, accounts, keys, manager, token)
	}
}
func newToken(key *ecdsa.PrivateKey, conn *ethclient.Client, registry *contracts.Registry) (mgrAddress common.Address, tokenAddr common.Address) {
	auth := bind.NewKeyedTransactor(key)
	tokenAddr, tx, _, err := contracts.DeployHumanStandardToken(auth, conn, big.NewInt(5000000000000000), "test", 2, "test symoble")
	if err != nil {
		log.Fatalf("Failed to DeployHumanStandardToken: %v", err)
	}
	fmt.Printf("token deploy tx=%s\n", tx.Hash().String())
	ctx := context.Background()
	_, err = bind.WaitDeployed(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to deploy contact when mining :%v", err)
	}
	fmt.Printf("DeployHumanStandardToken complete...\n")
	tx, err = registry.AddToken(auth, tokenAddr)
	if err != nil {
		log.Fatalf("Failed to AddToken: %v", err)
	}
	ctx = context.Background()
	_, err = bind.WaitMined(ctx, conn, tx)
	if err != nil {
		log.Fatalf("failed to AddToken when mining :%v", err)
	}
	mgrAddress, err = registry.ChannelManagerByToken(nil, tokenAddr)
	fmt.Printf("DeployHumanStandardToken complete... %s,mgr=%s\n", tokenAddr.String(), mgrAddress.String())
	return
}
func transferMoneyForAccounts(key *ecdsa.PrivateKey, conn *ethclient.Client, accounts []common.Address, token *contracts.Token) {
	wg := sync.WaitGroup{}
	wg.Add(len(accounts))
	auth := bind.NewKeyedTransactor(key)
	nonce, err := conn.PendingNonceAt(context.Background(), auth.From)
	if err != nil {
		log.Fatalf("pending nonce err %s", err)
		return
	}
	for index, account := range accounts {
		go func(account common.Address, i int) {
			auth2 := bind.NewKeyedTransactor(key)
			auth2.Nonce = big.NewInt(int64(nonce) + int64(i))
			fmt.Printf("transfer to %s,nonce=%s\n", account.String(), auth2.Nonce)
			tx, err := token.Transfer(auth2, account, big.NewInt(5000000))
			if err != nil {
				log.Fatalf("Failed to Transfer: %v", err)
			}
			ctx := context.Background()
			_, err = bind.WaitMined(ctx, conn, tx)
			if err != nil {
				log.Fatalf("failed to Transfer when mining :%v", err)
			}
			fmt.Printf("Transfer complete...\n")
			wg.Done()
		}(account, index)
		time.Sleep(time.Millisecond * 100)
	}
	wg.Wait()
	for _, account := range accounts {
		b, err := token.BalanceOf(nil, account)
		if err != nil {
			log.Fatalf("balance of err %s", err)
		}
		log.Printf("account %s has token %s\n", utils.APex(account), b)
	}
}

//path A-B-C-F-B-D-G-E
func createChannels(conn *ethclient.Client, accounts []common.Address, keys []*ecdsa.PrivateKey, manager *contracts.ChannelManagerContract, token *contracts.Token) {
	if len(accounts) < 6 {
		panic("need 6 accounts")
	}
	AccountA := accounts[0]
	AccountB := accounts[1]
	AccountC := accounts[2]
	AccountD := accounts[3]
	AccountE := accounts[4]
	AccountF := accounts[5]
	AccountG := accounts[6]
	fmt.Printf("accountA=%s\naccountB=%s\naccountC=%s\naccountD=%s\naccountE=%s\naccountF=%s\naccountG=%s\n",
		AccountA.String(), AccountB.String(), AccountC.String(), AccountD.String(),
		AccountE.String(), AccountF.String(), AccountG.String())
	keyA := keys[0]
	keyB := keys[1]
	keyC := keys[2]
	keyD := keys[3]
	keyE := keys[4]
	keyF := keys[5]
	keyG := keys[6]
	//fmt.Printf("keya=%s,keyb=%s,keyc=%s,keyd=%s,keye=%s,keyf=%s,keyg=%s\n", keyA, keyB, keyC, keyD, keyE, keyF, keyG)
	createchannel.CreatAChannelAndDeposit(AccountA, AccountB, keyA, keyB, 100, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountB, AccountD, keyB, keyD, 90, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountB, AccountC, keyB, keyC, 50, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountB, AccountF, keyB, keyF, 70, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountC, AccountF, keyC, keyF, 60, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountC, AccountE, keyC, keyE, 10, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountD, AccountG, keyD, keyG, 90, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountG, AccountE, keyG, keyE, 80, manager, token, conn)

}

func createChannels2(conn *ethclient.Client, accounts []common.Address, keys []*ecdsa.PrivateKey, manager *contracts.ChannelManagerContract, token *contracts.Token) {
	if len(accounts) < 6 {
		panic("need 6 accounts")
	}
	AccountA := accounts[0]
	AccountB := accounts[1]
	AccountC := accounts[2]
	AccountD := accounts[3]
	AccountE := accounts[4]
	AccountF := accounts[5]
	fmt.Printf("accountA=%saccountB=%saccountC=%saccountD=%saccountE=%saccountF=%s\n", AccountA.String(), AccountB.String(), AccountC.String(), AccountD.String(), AccountE.String(), AccountF.String())
	keyA := keys[0]
	keyB := keys[1]
	keyC := keys[2]
	keyD := keys[3]
	keyE := keys[4]
	keyF := keys[5]
	fmt.Printf("keya=%s,keyb=%s,keyc=%s,keyd=%s,keye=%s,keyf=%s\n", keyA, keyB, keyC, keyD, keyE, keyF)
	createchannel.CreatAChannelAndDeposit(AccountA, AccountB, keyA, keyB, 100, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountB, AccountD, keyB, keyD, 50, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountA, AccountC, keyA, keyC, 90, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountC, AccountE, keyC, keyE, 80, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountE, AccountD, keyE, keyD, 70, manager, token, conn)

}

/*
registry address :0x0C31cF985eA2F2932c2EDF05f36aBC7b24B17d40 test poa net networkid :8888
you find this topology at the above address
*/
func createChannels3(conn *ethclient.Client, accounts []common.Address, keys []*ecdsa.PrivateKey, manager *contracts.ChannelManagerContract, token *contracts.Token) {
	if len(accounts) < 6 {
		panic("need 6 accounts")
	}
	AccountA := accounts[0]
	AccountB := accounts[1]
	AccountC := accounts[2]
	AccountD := accounts[3]
	AccountE := accounts[4]
	AccountF := accounts[5]
	fmt.Printf("accountA=%saccountB=%saccountC=%saccountD=%saccountE=%saccountF=%s\n", AccountA.String(), AccountB.String(), AccountC.String(), AccountD.String(), AccountE.String(), AccountF.String())
	keyA := keys[0]
	keyB := keys[1]
	keyC := keys[2]
	keyD := keys[3]
	keyE := keys[4]
	keyF := keys[5]

	createchannel.CreatAChannelAndDeposit(AccountA, AccountB, keyA, keyB, 100, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountB, AccountC, keyB, keyC, 90, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountC, AccountD, keyC, keyD, 50, manager, token, conn)

	createchannel.CreatAChannelAndDeposit(AccountB, AccountE, keyB, keyE, 80, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountE, AccountF, keyE, keyF, 70, manager, token, conn)
	createchannel.CreatAChannelAndDeposit(AccountF, AccountD, keyF, keyD, 60, manager, token, conn)

}
func createChannels4(conn *ethclient.Client, accounts []common.Address, keys []*ecdsa.PrivateKey, manager *contracts.ChannelManagerContract, token *contracts.Token) {
	if len(accounts) < 6 {
		panic("need 6 accounts")
	}
	AccountA := accounts[0]
	AccountB := accounts[1]
	AccountC := accounts[2]
	AccountD := accounts[3]
	AccountE := accounts[4]
	AccountF := accounts[5]
	fmt.Printf("accountA=%saccountB=%saccountC=%saccountD=%saccountE=%saccountF=%s\n", AccountA.String(), AccountB.String(), AccountC.String(), AccountD.String(), AccountE.String(), AccountF.String())
	keyA := keys[0]
	keyB := keys[1]
	keyC := keys[2]
	keyD := keys[3]
	keyE := keys[4]
	keyF := keys[5]
	/*
	   4.1 create channel A-B and save 100 both

	*/
	createchannel.CreatAChannelAndDeposit(AccountA, AccountB, keyA, keyB, 100, manager, token, conn)
	/*
	 4.2 create channel B-C and save 50 both
	*/
	createchannel.CreatAChannelAndDeposit(AccountB, AccountC, keyB, keyC, 50, manager, token, conn)
	/*
	  4.3 create channel C-E and save 100 both
	*/

	createchannel.CreatAChannelAndDeposit(AccountC, AccountE, keyC, keyE, 100, manager, token, conn)

	/*
	   4.4 create channel A-D and save 100 both
	*/

	createchannel.CreatAChannelAndDeposit(AccountA, AccountD, keyA, keyD, 100, manager, token, conn)

	/*
	  4.5 create channel B-D and save 100 both
	*/

	createchannel.CreatAChannelAndDeposit(AccountB, AccountD, keyB, keyD, 100, manager, token, conn)

	/*
	   4.6 create channel D-F and save 100 both
	*/

	createchannel.CreatAChannelAndDeposit(AccountD, AccountF, keyD, keyF, 100, manager, token, conn)

	/*
	   4.7 create channel F-E and save 100 both
	*/

	createchannel.CreatAChannelAndDeposit(AccountF, AccountE, keyF, keyE, 100, manager, token, conn)
	/*
	   4.8 create channel C-F and save 50 both
	*/

	createchannel.CreatAChannelAndDeposit(AccountC, AccountF, keyC, keyF, 50, manager, token, conn)

	/*
	   4.9     D-E 100
	*/

	createchannel.CreatAChannelAndDeposit(AccountD, AccountE, keyD, keyE, 100, manager, token, conn)

}