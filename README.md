# Dagger Compose

Runs `docker-compose.yml`... but in Dagger. 📢💨📢💨 (those are airhorns)

Currently uses a VERY hacky host-to-container implementation.

## Example

The included `docker-compose.yml` runs WordPress, published to port 8080 on the
host.

```sh
go run .
```

## Thanks

Thanks to [**@marcosnils**](https://github.com/marcosnils) for the idea!
