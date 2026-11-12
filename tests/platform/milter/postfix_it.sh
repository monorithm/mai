# Runs a real Postfix and Dovecot SASL in the mai:dev image against the milter.
set -e
which python3 postconf postfix >/dev/null
postconf -d mail_version
mkdir -p /tmp/pf /tmp/pfq /tmp/pfd
cat > /tmp/pf/main.cf <<'X'
compatibility_level = 3.6
queue_directory = /tmp/pfq
data_directory = /tmp/pfd
mail_owner = postfix
myhostname = mail.mai.internal
mydomain = mai.internal
mynetworks = 127.0.0.0/8
inet_interfaces = loopback-only
inet_protocols = ipv4
maillog_file = /dev/stdout
smtpd_relay_restrictions = permit_mynetworks, reject
default_transport = discard
local_transport = discard
X
cat >> /tmp/pf/main.cf <<'X'
smtpd_sasl_type = dovecot
smtpd_sasl_path = private/auth
smtpd_sasl_auth_enable = yes
X
mkdir -p /tmp/dv
cat > /tmp/dv/dovecot.conf <<'X'
dovecot_config_version = 2.4.0
dovecot_storage_version = 2.4.0
protocols =
base_dir = /tmp/dv/run
auth_mechanisms = plain login
auth_allow_cleartext = yes
passdb passwd-file {
  passwd_file_path = /tmp/dv/passwd
}
service auth {
  unix_listener /tmp/pfq/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}
X
echo 'cursor:{PLAIN}testpw' > /tmp/dv/passwd
cat > /tmp/pf/master.cf <<'X'
127.0.0.1:2525 inet n - n - - smtpd
  -o smtpd_milters=unix:private/mai-milter
  -o milter_default_action=tempfail
  -o milter_protocol=6
pickup    unix  n       -       n       60      1       pickup
cleanup   unix  n       -       n       -       0       cleanup
qmgr      unix  n       -       n       300     1       qmgr
rewrite   unix  -       -       n       -       -       trivial-rewrite
bounce    unix  -       -       n       -       0       bounce
defer     unix  -       -       n       -       0       bounce
trace     unix  -       -       n       -       0       bounce
verify    unix  -       -       n       -       1       verify
flush     unix  n       -       n       1000?   0       flush
proxymap  unix  -       -       n       -       -       proxymap
smtp      unix  -       -       n       -       -       smtp
showq     unix  n       -       n       -       -       showq
error     unix  -       -       n       -       -       error
discard   unix  -       -       n       -       -       discard
anvil     unix  -       -       n       -       1       anvil
scache    unix  -       -       n       -       1       scache
postlog   unix-dgram n  -       n       -       1       postlogd
X
postfix -c /tmp/pf set-permissions >/dev/null 2>&1 || true
postfix -c /tmp/pf start 2>&1 | tail -2 || true
mkdir -p /tmp/pfq/private
chown postfix /tmp/pfq/private
su -s /bin/sh postfix -c "PYTHONPATH=/src python3 -m mai.app_milter --no-envelope --socket /tmp/pfq/private/mai-milter" &
dovecot -c /tmp/dv/dovecot.conf || true
sleep 2
ls -l /tmp/pfq/private/mai-milter
python3 - <<'P'
import smtplib
def go(frm, auth=True):
    s=smtplib.SMTP("127.0.0.1",2525); s.ehlo()
    if auth: s.login("cursor","testpw")
    try:
        s.sendmail("cursor@mai.internal",["b@mai.internal"],"From: %s\r\nSubject: t\r\n\r\nhi\r\n"%frm)
        r="accepted"
    except smtplib.SMTPDataError as e:
        r="%d %s"%(e.smtp_code, e.smtp_error.decode())
    s.quit(); print("RESULT", repr(frm), auth, "->", r)
go("cursor@mai.internal")
go("Cursor Agent <cursor@mai.internal>")
go("owner@mai.internal")
go("cursor@mai.internal", auth=False)
P
sleep 1
