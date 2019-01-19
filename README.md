# cashAccountDBd
cashAccountDBd is for indexing CashAccount-Transations into a SQLite-Database

More about CashAccount: https://gitlab.com/cash-accounts/specification/blob/master/SPECIFICATION.md


### 1. Installation
Install git and golang via the package manager.
```
sudo apt-get install git golang
```

Install cashAccountDBd via go get
```
go get github.com/abck/cashAccountDBd
```

Run cashAccountDBd once to create the default configuration.
```
~/go/bin/cashAccountDBd
```

Edit the configuration, located at `~/.cashAccount/cashAccountDBd.conf`
Sample configuration is:
```
[cashAccountDBd]
rpchost=127.0.0.1:8334
rpcendpoint=ws
rpcuser=USERNAME
rpcpass=PASSWORD
```

Create a new folder for the bchd-server cert
```
mkdir ~/.bchd/
```
and copy the `rpc.cert` into that folder. 
(You can find the cert in `~/bchd/` of the user running bchd, if you are not running bchd on your machine, ask the server owner for the cert)

Start the indexing server:
```
~/go/bin/cashAccountDBd
```

cashAccountDBd is now running.
You should now see an output like this:
```
2019/01/19 18:47:12 Creating new database...
2019/01/19 18:47:12 Connecting to bchd...
2019/01/19 18:47:12 Current database blockheight: 563719
2019/01/19 18:47:12 Current node blockheight: 566016
2019/01/19 18:47:12 Parsing block 563720/566016
2019/01/19 18:47:12 Parsing block 563721/566016
[...]
2019/01/19 18:51:33 Parsing block 566015/566016
2019/01/19 18:51:33 Parsing block 566016/566016
2019/01/19 18:51:33 Database is up to date!
2019/01/19 18:51:33 Waiting for new blocks...
2019/01/19 18:52:35 New block: 566017
```

### 2. SQLite-Database
The database is located at `~/.cashAccount/db.sqlite`
##### nameindex
block | name | txid
--- | --- | ---
`integer` | `text` | `blob`
blockheight | Cash Account name | txid as binary data

##### status

name | data
--- | --- 
`text` | `text`
current fields in the status table are `BlockHeight` and `Version`

