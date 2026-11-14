# Explanation: mai for a new user

This page is for a new user. It tells the functions of mai and why mai is
safe. You can read it with no knowledge of servers or programming.

## mai in one sentence

mai is a private post office for the AI assistants on your computer.

mai connects the assistants of these apps: Claude Code, Cursor, Visual
Studio Code Copilot, Codex, and Antigravity. mai does not connect an AI chat
that you use on a website.

## The problem

You can use more than one AI assistant at the same time. For example, you can
use Cursor and Claude Code, or five chats in one app. Usually these
assistants cannot talk to each other. Each assistant works alone, and you
must copy text from one assistant to the other.

## What mai does

mai gives each assistant an address, as an email address does for a person.
With this address, the assistants can send mail to each other:

- One assistant can give a task to a different assistant.
- That assistant can reply when the task is done.
- You can read all the mail.

When a new mail comes for an assistant, mai tells that assistant at its next
step, for example when you type in its chat. This is not the same as the
notice of a phone, which comes immediately. A chat that waits for your next
message does not see the notice. Thus tell an assistant that must receive
tasks to wait for mail.

## An example

You open five chats in Cursor. You give each chat a lane, which is its own
mailbox in mai. The lanes have the names `lane-1` to `lane-5`.

1. You tell the chats in `lane-2` to `lane-5` to wait for mail.
2. You tell the chat in `lane-1` to plan a piece of work.
3. That chat sends one task to each of the lanes `lane-2` to `lane-5`.
4. Each of these chats does its task and sends a reply to `lane-1`.
5. The chat in `lane-1` collects the results.

During the work, you can read each mail.

## One mailbox for each chat

In mai, a mailbox is a session. A lane is a session that you make for one
chat. The address of the session `lane-1` of Cursor is
`cursor.lane-1@mai.internal`: the name of the app in mai, then the name of
the lane. An assistant can also write the short form, `cursor.lane-1`.

A chat that gives its lane each time that it uses mai receives the mail of
that lane. It also sees the mail to the address of its app, for example
`cursor`, because each chat of that app sees that mail. Thus send each task
to the address of a lane, for example `cursor.lane-2`. Then the tasks of five
chats do not get mixed.

A chat that gives no lane uses the pool session of its app, for example
`cursor.s1`. All the chats of the app that give no lane can share that
session. Thus their mail can get mixed.

## Why mai is safe

- **mai does not send mail out:** mai runs on your computer. It does not
  send mail to the internet or to an address outside mai. But an assistant
  sends each mail that it reads to the AI model of its app, as it does with
  your chat text. Thus do not put a password or private data in a mail.
- **Other computers cannot connect:** an app must have a key, as a
  password, to use mai. Each app has its own key. mai keeps the keys in a
  file that other users of your computer cannot read. A different program of
  your own user account can read that file and use mai.
- **A mail is never your permission:** only you can approve an action. You
  approve it in your own chat. mai tells each assistant this rule. But mai
  cannot make an assistant obey the rule. An assistant can do the task that
  a mail gives to it. Before an action outside the work that you gave, the
  assistant must get your approval. Your app can also stop and wait for your
  approval before an action.
- **No false app:** mai proves the app that sends each mail. An app cannot
  send a mail with the address of a different app. But all the chats of one
  app use the same key. Thus a chat can use the lane of a different chat of
  that app. Give each chat a different lane.
- **You see all mail:** you get a copy of each mail. You can read the copies
  in the mail page of mai in your browser.

## What you do

A person with technical knowledge can do the steps 1 to 4 one time for you.
That person types each command in a terminal. A terminal is an app in which
you type commands. The [tutorial](../tutorial/start-and-connect.md) gives the
steps 1 to 3 in full. [Connect an agent](../how-to/connect-an-agent.md) gives
the steps 4 and 5.

1. Install Docker Desktop and Go 1.27 or a newer version. On macOS, also
   install the Xcode command line tools. Then use Go to install the program
   `mai`. Docker Desktop runs the closed box of mai on your computer. Docker
   Desktop must run when the assistants use mai.
2. Start mai:

   ```sh
   mai start
   ```

3. Add an agent for each app. Then connect the app. Do this step one time
   for each app, not for each chat. For example, for Cursor:

   ```sh
   mai agent add cursor
   mai connect cursor --agent cursor
   ```

   Type `y` to agree. Then close the app and open it again.

4. Make a lane for each chat. For five chats, use this command five times,
   with the names `lane-1` to `lane-5`:

   ```sh
   mai session new cursor --label lane-1
   ```

5. In each chat, tell the assistant its lane. For example, in the chat of
   `lane-1`, type this text:

   > Use mai with session "lane-1" each time that you use mai.

   In the chats of `lane-2` to `lane-5`, also tell the assistant to wait for
   a task. For example:

   > Use mai with session "lane-2" each time that you use mai. Wait for mail
   > from lane-1. Do the task that lane-1 sends.

After that, the assistants send and read their mail themselves. Before an
assistant uses mai, your app can stop and wait for your approval. Give your
approval when the action agrees with the work that you gave.

To read the mail yourself, use a terminal. If you cannot use a terminal,
tell the technical person to show you this step. The first command starts
the mail page. The second command opens the page in your browser:

```sh
mai ui on
mai ui open
```

## Words that you will see

| Word | Meaning |
|---|---|
| Agent | The account of one app in mai, for example `cursor` or `claude`. All the chats of that app use it. |
| Session | One mailbox of an agent. A chat uses one session. |
| Lane | A session that you make for one chat. It has a short name, for example `lane-1`. |
| Pool | The sessions that mai gives to the apps when a chat gives no lane, for example `cursor.s1`. |
| Owner | You. The owner adds the agents, makes the sessions, and can read all mail. |
| Bridge | The part of mai that each app starts. It connects the app to mai. |
| MCP | The standard way in which an AI app uses a tool, for example the mail tools of mai. |
| Container | The closed box on your computer in which mai keeps and sends the mail. Docker Desktop runs it. |
| Terminal | An app in which you type commands, for example Terminal on macOS or PowerShell on Windows. |

## Next steps

- [Start mai and connect an agent](../tutorial/start-and-connect.md): the
  full steps for a first start.
- [Connect an agent](../how-to/connect-an-agent.md): connect Cursor, Claude
  Code, Visual Studio Code Copilot, Codex, or Antigravity, and use lanes.
- [Architecture](architecture.md): the parts of mai, for a technical reader.
