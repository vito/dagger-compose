# Dagger Compose

Runs `docker-compose.yml`... but in Dagger. 📢💨📢💨 (those are airhorns)

Currently uses a proof-of-concept host-to-container implementation.

## Example

The included `wordpress.yml` runs WordPress, published to port 8080 on the
host.

```sh
go run . wordpress.yml
```

## Thanks

Thanks to [**@marcosnils**](https://github.com/marcosnils) for the idea!
