## Self-Registration (Signup)

Stowr allows you to enable user self-registration (signup). This can be enabled via **Settings → Global Settings**, or with `stowr config set --signup`. Self-registered users inherit the configured **user defaults**, including the scope.

> [!WARNING]
>
> By default, the user scope is the server's root, so a self-registered user could read,
> modify, and delete every file Stowr serves. To prevent this, either:
>
> a. Enable `createUserDir` so each user gets their own directory; or
> b. If users are meant to share files, set the default scope to something other than the root.

## Fail2ban

Stowr does not natively support protection against brute force attacks. Therefore, we suggest using something like [fail2ban](https://github.com/fail2ban/fail2ban), which takes care of that by tracking the logs of your Stowr instance. For more information on how fail2ban works, please refer to their [wiki](https://github.com/fail2ban/fail2ban/wiki).

### Filter Configuration

An example filter configuration targeted at matching Stowr's logs.

```ini
[INCLUDES]
before = common.conf

[Definition]
datepattern = `^%%Y\/%%m\/%%d %%H:%%M:%%S`
failregex   = `\/api\/login: 403 <HOST> *`
```

### Jail Configuration

An example jail configuration. You should fill it with the path of the logs of Stowr, as well as the port where it is running at.

```ini
[filebrowser]

enabled = true
port = [your_port]
filter = filebrowser
logpath = [your_log_path]
maxretry = 10
bantime = 10m
findtime = 10m
banaction = iptables-allports
banaction_allports = iptables-allports
```
