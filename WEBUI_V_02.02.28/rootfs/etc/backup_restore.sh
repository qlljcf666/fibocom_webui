#!/bin/sh


backupfile="/etc/backup.list"
target_path="/tmp/tarcfg"
package_path="/tmp/cfg.tar.gz"
checkfile_path="${target_path}/checkcfg.test.key"

if [ ! -e ${backupfile} ]; then
    echo "the ${backupfile} is not exist! stop backup."
    exit 1
fi

echo $1

function do_package_tar()
{
    while read path
    do
#        echo $path
        cleanpath=$(echo $path | sed 's/[[:space:]]//g')
#       echo $cleanpath
        if [ -n $cleanpath ]; then
            cp -rf $cleanpath  ${target_path}
        fi
    done < ${backupfile}
    echo `date` > ${checkfile_path}
    cd ${target_path}
    tar -czf ${package_path} *
    rm -rf ${target_path}
}

function do_package_unpress_cover()
{
    if [ ! -e ${package_path} ]; then
        echo "the package is not exist!!"
        exit 1
    fi

    tar -zxf ${package_path} -C ${target_path}
    if [ "$?" != "0" ]; then
        echo "tar this ${package_path} failed!!"
        exit 1
    fi
    
    if [ ! -e ${checkfile_path} ]; then
        echo "checkfile is not exist"
        exit 1
    fi
    cd ${target_path}
    echo "CurrentPath $(pwd)"
    while read path
    do
#       echo $path
        cleanpath=$(echo $path | sed 's/[[:space:]]//g')
#       echo $cleanpath
        if [ -n $cleanpath ]; then
            dir_file=$(basename $cleanpath)
            echo $dir_file
            if [ -f $cleanpath ]; then
                echo "$cleanpath is file!"
                cp -rf $dir_file $cleanpath
            elif [ -d $cleanpath ]; then
                echo "$cleanpath is dir!"
                cp -rf $dir_file/* $cleanpath
            else
                echo "$cleanpath is not exist!"
            fi
#           cp -rf $path  ${target_path}
        fi
    done < ${backupfile}
    rm -rf ${target_path}
}

echo ${target_path}
echo ${backupfile}

if [ -d ${target_path} ]; then
    rm -rf  ${target_path}
    mkdir -p ${target_path}
else
    mkdir -p ${target_path}
fi

case $1 in 
    backup)
        echo "Now we need to backup it from the backup.list"
        do_package_tar
        if [ "$?" == "0" ]; then
            echo "Now backup done!"
        else
            echo "Now backup failed!!!"
            exit 1
        fi
        ;;

    restore)
        echo "Now we need to restore the package cover the current config."
        do_package_unpress_cover
        if [ "$?" == "0" ]; then
            echo "Now restore done!"
        else
            echo "Now restore failed!!!"
            exit 1
        fi
        ;;
    *)
        echo "Help: $0 {backup|restore}"
        echo "Note: dos2unix ${backupfile}"
        rm -rf  ${target_path}
        exit 1
        ;;
esac

