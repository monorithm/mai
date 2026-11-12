from bustan import FactoryProvider, Module

from .mail_client_factory import MailClientFactory


# Each module that reads or sends mail imports this module. The factory opens
# one MailSession for each login. A test overrides MailClientFactory with a
# fake, thus no host test needs Dovecot or Postfix.
@Module(
    providers=[
        FactoryProvider(provide=MailClientFactory, use_factory=MailClientFactory)
    ],
    exports=[MailClientFactory],
)
class MailServerModule:
    pass
