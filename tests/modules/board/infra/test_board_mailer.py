from mai.modules.board.infra.board_mailer import BoardMailer


class FakeSession:
    def __init__(self, fail):
        self.fail, self.sent, self.closed = fail, [], False

    def connect(self):
        return self

    def send(self, raw, mail_from, rcpt):
        if self.fail:
            raise OSError("down")
        self.sent.append((mail_from, rcpt))

    def close(self):
        self.closed = True


class FakeFactory:
    def __init__(self, fail=False):
        self.fail, self.opened = fail, []

    def open(self, login, secret):
        self.opened.append(login)
        session = FakeSession(self.fail)
        self.session = session
        return session


def _cred(tmp_path):
    path = tmp_path / "board.cred"
    path.write_text("mai1:board:" + "a" * 26 + "\n")
    return path


def test_sends_as_board(tmp_path):
    factory = FakeFactory()
    assert BoardMailer(factory, _cred(tmp_path)).send(b"x", ["cursor"])
    assert factory.opened == ["board"]
    assert factory.session.sent == [("board@mai.internal", ["cursor@mai.internal"])]


def test_failure_returns_false(tmp_path):
    factory = FakeFactory(fail=True)
    assert BoardMailer(factory, _cred(tmp_path)).send(b"x", ["cursor"]) is False
    assert len(factory.opened) == 2


def test_missing_credential_returns_false(tmp_path):
    assert (
        BoardMailer(FakeFactory(), tmp_path / "none.cred").send(b"x", ["cursor"])
        is False
    )
