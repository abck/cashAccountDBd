package main

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"github.com/gcash/bchd/rpcclient"
	"github.com/gcash/bchd/wire"
	"github.com/gcash/bchutil"
	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/ini.v1"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
)


var client *rpcclient.Client
var update chan BlockUpdateMsg

type BlockUpdateMsg struct {
	blockHeight int64

	// true = block added to blockchain
	// false = block dropped from blockchain
	msgType bool
}

const defaultConfig string = "[cashAccountDBd]\nrpchost=127.0.0.1:8334\nrpcendpoint=ws\nrpcuser=\nrpcpass="
type Config struct {
	cashAccountDBd struct {
		rpchost string
		rpcendpoint string
		rpcuser string
		rpcpass string
	}
}

func main() {
	update = make(chan BlockUpdateMsg)

	ntfnHandlers := rpcclient.NotificationHandlers{
		OnFilteredBlockConnected: EventOnFilteredBlockConnected,
		OnFilteredBlockDisconnected: EventOnFilteredBlockDisconnected,
	}

	// check if database dir exists, if not try to create it
	cashaccHomeDir := bchutil.AppDataDir("cashAccount", false)
	if _, err := os.Stat(cashaccHomeDir); os.IsNotExist(err) {
		err = os.MkdirAll(cashaccHomeDir, os.ModePerm)
		if err != nil {
			log.Fatal(err)
		}
	}

	// check if config exists, if it does not, write default config to file
	cfgFile := filepath.Join(cashaccHomeDir, "cashAccountDBd.conf")
	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		err = ioutil.WriteFile(cfgFile,[]byte(defaultConfig), 0600)
		if err != nil {
			log.Fatal(err)
		}
	}

	// read config
	cfg, err := ini.Load(cfgFile)
	if err != nil {
		log.Fatal(err)
	}

	// basic config check
	if cfg.Section("cashAccountDBd").Key("rpchost").String() == "" ||
		cfg.Section("cashAccountDBd").Key("rpcuser").String() == "" ||
		cfg.Section("cashAccountDBd").Key("rpcpass").String() == "" ||
		cfg.Section("cashAccountDBd").Key("rpcendpoint").String() == ""  {
		log.Fatal("Configuration not set up. Please set up the configuration! File:" + cfgFile)
	}


	// check if database exists, if not, try to create one
	cashaccDbFileName := filepath.Join(cashaccHomeDir, "db.sqlite")
	if _, err := os.Stat(cashaccDbFileName); os.IsNotExist(err) {
		log.Println("Creating new database...")
		db, err := sql.Open("sqlite3", cashaccDbFileName)
		if err != nil {
			log.Fatal(err)
		}
		_, err = db.Exec(
			`create table nameindex (block integer not null, name text not null, txid blob not null); 
create table status (name text not null primary key, data text not null);
insert into status(name, data) values ("BlockHeight","563719");
insert into status(name, data) values ("Version","1");`)
		if err != nil {
			log.Fatal(err)
		}
		err = db.Close()
		if err != nil {
			log.Fatal(err)
		}
	}

	// bchd configuration
	log.Println("Connecting to bchd...")
	bchdHomeDir := bchutil.AppDataDir("bchd", false)
	certs, err := ioutil.ReadFile(filepath.Join(bchdHomeDir, "rpc.cert"))
	if err != nil {
		log.Fatal(err)
	}
	connCfg := &rpcclient.ConnConfig{
		Host:         cfg.Section("cashAccountDBd").Key("rpchost").String(),
		Endpoint:     cfg.Section("cashAccountDBd").Key("rpcendpoint").String(),
		User:         cfg.Section("cashAccountDBd").Key("rpcuser").String(),
		Pass:         cfg.Section("cashAccountDBd").Key("rpcpass").String(),
		Certificates: certs,
	}
	client, err := rpcclient.New(connCfg, &ntfnHandlers)
	if err != nil {
		log.Fatal(err)
	}


	// open database
	db, err := sql.Open("sqlite3", cashaccDbFileName)
	if err != nil {
		log.Fatal(err)
	}

	// check sqlite db version
	dbVersion, err := getDbVersion(db)
	if err != nil {
		log.Fatal(err)
	}
	if dbVersion != 1 {
		log.Fatal("Wrong database version!")
	}

	err = syncDbToNode(client, db)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Database is up to date!")

	err = client.NotifyBlocks()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Waiting for new blocks...")

	for{
		updateMsg := <- update
		if updateMsg.msgType == true{
			log.Printf("New block: %v\n", updateMsg.blockHeight)
			err = parseBlock(client, db, updateMsg.blockHeight)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			err = dropBlocks(db, updateMsg.blockHeight)
			if err != nil {
				log.Fatal(err)
			}
			err = syncDbToNode(client, db)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

func syncDbToNode(client *rpcclient.Client, db *sql.DB) error{
	dbBlockHeight, err := getDbBlockHeight(db)
	if err != nil {
		return err
	}
	nodeBlockHeight, err := getNodeBlockHeight(client)
	if err != nil {
		return err
	}
	log.Printf("Current database blockheight: %v\n", dbBlockHeight)
	log.Printf("Current node blockheight: %v\n", nodeBlockHeight)

	// parse new blocks
	for nodeBlockHeight > dbBlockHeight {

		log.Printf("Parsing block %v/%v\n", dbBlockHeight+1,nodeBlockHeight)

		err := parseBlock(client,db,dbBlockHeight+1)
		if err != nil {
			return err
		}

		dbBlockHeight, err = getDbBlockHeight(db)
		if err != nil {
			return err
		}
		nodeBlockHeight, err = getNodeBlockHeight(client)
		if err != nil {
			return err
		}
	}
	return nil
}

func checkName(name []byte) bool{
	for _, chr := range name{
		if !((chr >= 0x30 && chr <= 0x39) || (chr >= 0x41 && chr <= 0x5a) || (chr >= 0x61 && chr <= 0x7a) || chr == 0x5f)  {
			return false
		}
	}
	return true
}


func parseBlock(client *rpcclient.Client, db *sql.DB, blockHeight int64) error {

	// check blockHeight: if db blockHeight is higher, silent end (could happen is >1 block gets orphaned)
	dbBlockHeight, err := getDbBlockHeight(db)
	if err != nil {
		return err
	}
	if dbBlockHeight >= blockHeight{
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("insert into nameindex (block, name, txid) values(?, ?, ?);")
	if err != nil {
		return err
	}
	defer stmt.Close()

	blockHash, err := client.GetBlockHash(blockHeight)
	if err != nil {
		return err
	}
	block, err := client.GetBlock(blockHash)
	if err != nil {
		return err
	}
	transactions := block.Transactions

	for _, tx := range transactions {
		for _, txout := range tx.TxOut {

			// check for minimal size (1 return + 1 push + 4 protocol + 1 push + 1 name + 1 push + 2 data (1 type and 1 payload)
			if len(txout.PkScript) < 11 {
				continue
			}

			// check prefix
			if bytes.Compare([]byte("\x6a\x04\x01\x01\x01\x01"), txout.PkScript[0:6]) != 0 {
				continue
			}

			// check if OP_PUSH is greater than the max 99 char
			if 0x63 < txout.PkScript[6] && txout.PkScript[6] != 0x00 {
				continue
			}

			// check if script as least as long as the op_return says
			if len(txout.PkScript) < 7+int(txout.PkScript[6]) {
				continue
			}

			// read name, check name against charset 0-9a-zA-Z_
			txName := txout.PkScript[7 : 7+int(txout.PkScript[6])]
			if !checkName(txName){
				continue
			}

			txIdRaw, err := hex.DecodeString(tx.TxHash().String())
			if err != nil {
				return err
			}

			// add to db
			_, err = stmt.Exec(blockHeight,txName,txIdRaw)
			if err != nil {
				return err
			}
		}
	}

	stmt2, err := tx.Prepare(`UPDATE status SET data = ?	WHERE name = "BlockHeight";`)
	if err != nil {
		return err
	}
	defer stmt2.Close()

	_, err = stmt2.Exec(strconv.FormatInt(blockHeight, 10))
	if err != nil {
		return err
	}

	// write to db
	err = tx.Commit()
	if err != nil {
		return err
	}
	return nil
}

func getNodeBlockHeight(client *rpcclient.Client) (int64, error) {
	_, nodeBlockHeight, err := client.GetBestBlock()
	if err != nil {
		return 0, err
	}
	return int64(nodeBlockHeight), nil
}

func getDbBlockHeight(db *sql.DB) (int64, error) {
	tmp := ""
	err := db.QueryRow(`select data from status where name = "BlockHeight";`).Scan(&tmp)
	if err != nil {
		return 0, err
	}
	dbBlockHeight, err := strconv.ParseInt(tmp,10,32)
	if err != nil {
		return 0, err
	}

	return int64(dbBlockHeight), nil
}

func getDbVersion(db *sql.DB) (int64, error) {
	tmp := ""
	err := db.QueryRow(`select data from status where name = "Version";`).Scan(&tmp)
	if err != nil {
		return 0, err
	}
	dbVersion, err := strconv.ParseInt(tmp,10,32)
	if err != nil {
		return 0, err
	}

	return int64(dbVersion), nil
}

// drops block <blockHeight> and above
func dropBlocks(db *sql.DB, blockHeight int64) error {
	tx, err := db.Begin()
	stmt, err := tx.Prepare(`UPDATE status SET data = ?	WHERE name = "BlockHeight";`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(strconv.FormatInt(blockHeight, 10))
	if err != nil {
		return err
	}

	stmt2, err := tx.Prepare(`DELETE FROM nameindex WHERE block >= ?;`)
	if err != nil {
		return err
	}
	defer stmt2.Close()

	_, err = stmt2.Exec(strconv.FormatInt(blockHeight, 10))
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func EventOnFilteredBlockDisconnected(height int32, header *wire.BlockHeader) {
	update <- BlockUpdateMsg{int64(height),false}
}

func EventOnFilteredBlockConnected(height int32, header *wire.BlockHeader, txns []*bchutil.Tx) {
	update <- BlockUpdateMsg{int64(height),true}
}