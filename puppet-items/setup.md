# Puppet Setup Guide

Puppet is the configuration management tool used in this project. This setup guide explains how to set up a basic Wordpress application, but will change to more project-specific modifications in the future.

## What's in this repo?

This repo contains the following files:
   * **install-wp.pp**
   * **manifests**
      * **conf.pp**
      * **db.pp**
      * **init.pp**
      * **web.pp**
      * **wp.pp**

**install.pp** is a single class used to apply the changes set by the new class on the system (in this case, the class 'wordpress').

**conf.pp** is the main configuration file for setting variables which the rest of the manifest files will use. This file requires the user to set the following information:
   * `$root_password = '[PASSWORD]'`
   * `$db_name = '[NAME]'`
   * `$db_user = '[USERNAME]`
   * `$db_user_password = '[DB_PASSWORD]'`
   * `$db_host = '[IP_ADDRESS]`

**db.pp** is the manifest for setting up MySQL. It installs a MySQL server and sets its root password, as well as creating a database for Wordpress and a Wordpress user with sufficient access privileges.

**init.pp** installs Wordpress, Apache and MySQL by applying their respective manifests, and displays messages confirming successful installation for each when **install-wp.pp** is applied.

**web.pp** controls the installation of Apache and PHP.

**wp.pp** copies contents of the Wordpress installation to `/var/www/`, where Apache configures its server files. It also creates the file `wp-config.php`.

## Setting up Wordpress using Puppet
