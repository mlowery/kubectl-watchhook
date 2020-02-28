# kubectl-watchhook

kubectl-watchhook is a kubectl plugin that establishes a watch and then calls a user-specified command for each watch event.

## Why

For a regular watch via kubectl, the events are not delineated. With kubectl-watchhook, your command is invoked once per watch event.

## Building

```sh
$ go build -o kubectl-watchhook github.com/mlowery/kubectl-watchhook/cmd
```

## Running

Shown in the below example is a shell script that reads the events and writes them one-per-file to disk. The final command shows an event-by-event diff using those files.

```sh
$ echo 'f(){ local i=$(cat /dev/stdin); local n=$(echo "$i" | grep -m1 " name: " | tr -d " " | cut -d: -f2); echo "$i" > $n-$(date +%s%N)-$1.yaml; }; f "${@-}"' > my-cmd.sh
$ chmod a+x my-cmd.sh
$ kubectl watchhook pod my-pod -- sh my-cmd.sh
$ prev=""; for f in my-pod*.yaml; do if [[ $prev ]]; then diff $prev $f; fi; prev=$f; done
```

## Starting Point for Code

Copy of [sample-cli-plugin](https://github.com/kubernetes/sample-cli-plugin).
