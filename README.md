## multi: a new approach to parallel commands

multi is a small tool for making many ssh connections or local executors at once.

It is very fast. It is a much simpler version of GNU parallel with the
additional thread-based (not process-based) ssh functionality.

You can pass two formats to each command:

%t - the thread id (unique id of each thread)
%i - the item if -i was enabled, this will be a unique line from stdin.

No attempt is made to guarantee thread/item uniformity; runs may change thread
ids for items between invocations.

If both count and input are specified, the longer length wins, with the input
being omitted for any items that are are not there when coordinating with the
count.

If count is specified in ssh mode, it will be multiplied by the host list; and
count invocations will be run on each host.

There is currently no concurrency limit; it's gated at the number of items you
pass it.

## Author

Erik Hollensbe <erik@hollensbe.org>
