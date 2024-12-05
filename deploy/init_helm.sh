#!/bin/bash

set -e

# preprocess the variables

function replaceVar() {
  for file in $(ls */helm/values.yaml); do
    key="#$1#"
    echo "process replace file $file, key $key, value $2"
    if [ "$(uname)" == "Darwin" ]; then
      sed -i "" "s?$key?$2?g" $file
    else
      sed -i "s?$key?$2?g" $file
    fi
  done
}

varFile="variables.txt"
if [ ! -f "$varFile" ]; then
  echo "variables.txt not exists"
  exit 1
fi

export -f replaceVar

cat $varFile | awk -F ':' '{print "replaceVar", $1, $2}' | "/bin/bash"