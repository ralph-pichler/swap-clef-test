package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/external"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethersphere/go-sw3/contracts-v0-2-3/simpleswapfactory"
)

var (
	backendURL = "http://localhost:8545"
)

type EthBackend interface {
	bind.ContractBackend
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

// WalletBackend is minimum needed from go-ethereums wallet abstraction to support swap functions
type WalletBackend interface {
	Accounts() []accounts.Account
	SignData(account accounts.Account, mimetype string, text []byte) ([]byte, error)
	SignTx(account accounts.Account, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error)
}

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	ethBackend, err := ethclient.Dial(backendURL)
	if err != nil {
		return err
	}

	var wallet WalletBackend
	wallet, err = external.NewExternalSigner("./config/clef.ipc")
	if err != nil {
		return err
	}

	return runChequebook(ethBackend, wallet)
}

func NewWalletTransactor(wallet WalletBackend, account accounts.Account) *bind.TransactOpts {
	return &bind.TransactOpts{
		From: account.Address,
		Signer: func(signer types.Signer, address common.Address, transaction *types.Transaction) (*types.Transaction, error) {
			if address != account.Address {
				return nil, errors.New("not authorized to sign this account")
			}
			return wallet.SignTx(account, transaction, nil)
		},
	}
}

func runChequebook(ethBackend EthBackend, wallet WalletBackend) error {
	account := wallet.Accounts()[0]
	opts := NewWalletTransactor(wallet, account)
	fmt.Printf("selecting account %s\n", account.Address.Hex())

	_, tx, erc20, err := simpleswapfactory.DeployERC20Mintable(opts, ethBackend)
	if err != nil {
		return err
	}

	erc20Address, err := bind.WaitDeployed(context.TODO(), ethBackend, tx)
	if err != nil {
		return err
	}

	factoryAddress, tx, factory, err := simpleswapfactory.DeploySimpleSwapFactory(opts, ethBackend, erc20Address)
	if err != nil {
		return err
	}

	_, err = bind.WaitDeployed(context.TODO(), ethBackend, tx)
	if err != nil {
		return err
	}

	fmt.Printf("deployed factory to %s\n", factoryAddress.Hex())

	tx, err = factory.DeploySimpleSwap(opts, opts.From, big.NewInt(0))
	if err != nil {
		return err
	}

	receipt, err := bind.WaitMined(context.TODO(), ethBackend, tx)
	if err != nil {
		return err
	}

	address := common.Address{}
	for _, log := range receipt.Logs {
		if event, err := factory.ParseSimpleSwapDeployed(*log); err == nil {
			address = event.ContractAddress
			break
		}
	}
	if (address == common.Address{}) {
		return errors.New("contract deployment failed")
	}

	fmt.Printf("deployed simpleswap to %s\n", address.Hex())

	tx, err = erc20.Mint(opts, address, big.NewInt(50000))
	if err != nil {
		return err
	}

	receipt, err = bind.WaitMined(context.TODO(), ethBackend, tx)
	if err != nil {
		return err
	}

	cheque := &ChequeParams{
		Contract:         address,
		Beneficiary:      account.Address,
		CumulativePayout: 100,
	}

	sig, err := wallet.SignData(account, accounts.MimetypeTextPlain, cheque.sigHash())
	if err != nil {
		return err
	}

	rec := common.HexToAddress("0xAd4F6Efc6594fE9305bF9A69BAb8bd942aDAECDB")

	tx, err = CashChequeBeneficiaryRequest(ethBackend, address, rec, cheque, sig)
	if err != nil {
		return err
	}

	tx, err = wallet.SignTx(account, tx, nil)
	if err != nil {
		return err
	}

	err = ethBackend.SendTransaction(context.Background(), tx)
	if err != nil {
		return err
	}

	receipt, err = bind.WaitMined(context.TODO(), ethBackend, tx)
	if err != nil {
		return err
	}

	fmt.Printf("got receipt with status %v\n", receipt.Status)

	b, err := erc20.BalanceOf(nil, rec)
	if err != nil {
		return err
	}

	fmt.Printf("balance: %v\n", b)

	return nil
}

// ChequeParams encapsulate all cheque parameters
type ChequeParams struct {
	Contract         common.Address // address of chequebook, needed to avoid cross-contract submission
	Beneficiary      common.Address // address of the beneficiary, the contract which will redeem the cheque
	CumulativePayout uint64         // cumulative amount of the cheque in currency
}

// encodeForSignature encodes the cheque params in the format used in the signing procedure
func (cheque *ChequeParams) encodeForSignature() []byte {
	cumulativePayoutBytes := make([]byte, 32)
	// we need to write the last 8 bytes as we write a uint64 into a 32-byte array
	// encoded in BigEndian because EVM uses BigEndian encoding
	binary.BigEndian.PutUint64(cumulativePayoutBytes[24:], cheque.CumulativePayout)
	// construct the actual cheque
	input := cheque.Contract.Bytes()
	input = append(input, cheque.Beneficiary.Bytes()...)
	input = append(input, cumulativePayoutBytes[:]...)
	return input
}

// sigHash hashes the cheque params using the prefix that would be added by eth_Sign
func (cheque *ChequeParams) sigHash() []byte {
	// we can ignore the error because it is always nil
	encoded := cheque.encodeForSignature()
	input := crypto.Keccak256(encoded)
	withPrefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(input), input)
	return crypto.Keccak256([]byte(withPrefix))
}

func CashChequeBeneficiaryRequest(backend EthBackend, to common.Address, recipient common.Address, cheque *ChequeParams, ownerSig []byte) (*types.Transaction, error) {
	abi, err := abi.JSON(strings.NewReader(simpleswapfactory.ERC20SimpleSwapABI))
	if err != nil {
		return nil, err
	}

	callData, err := abi.Pack("cashChequeBeneficiary", recipient, big.NewInt(int64(cheque.CumulativePayout)), ownerSig)
	if err != nil {
		return nil, err
	}

	nonce, err := backend.PendingNonceAt(context.Background(), cheque.Beneficiary)
	if err != nil {
		return nil, err
	}

	gasPrice, err := backend.SuggestGasPrice(context.Background())
	if err != nil {
		return nil, err
	}

	return types.NewTransaction(nonce, to, big.NewInt(0), 1000000, gasPrice, callData), nil
}
