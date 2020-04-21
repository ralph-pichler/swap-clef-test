# swap clef test

1. Initialization of clef configuration (in a custom directory). Enter "ok" and a password.

```sh
clef --configdir config init
```

2. Set the passwort for the key that's already in the keystore. The password is `signing-test`. This key is the first one derived from the seed further below.

```sh
clef --configdir config --keystore keys setpw d7943e06aa5055b79a8d3e4a4e39ce1f52e9a028
```

3. Attest the rules files. The hash is the `sha256sum` of `rules.js`.

```sh
clef --configdir config attest 108245a492b21a83ef9f63cf69b2353936a3bfb8af8f442bd49bd5798c3d869a
```

4. Start the clef signing service with the correct ruleset. Because the swap chequebook functions are not in go-ethereum's 4byte directory, a custom 4byte directory needs to be specified. Note that clef will start even if the master password is entered incorrectly but with the rules disabled.

```sh
clef --configdir config --chainid 12345 --keystore keys --4bytedb-custom 4byte.json --rules rules.js
```

5. Start a ganache instance

```sh
ganache-cli -n 12345 -m "neglect opera square stable dance myth legend aspect bright whip snap quote"
```

6. Run the test script

```go
go run ./main
```