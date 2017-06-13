# Puppet Setup Guide

Puppet is the configuration management tool used in this project. This setup guide explains how to set up a basic Wordpress application based on a [guide from DigitalOcean](https://www.digitalocean.com/community/tutorials/how-to-create-a-puppet-module-to-automate-wordpress-installation-on-ubuntu-14-04), but will change to more project-specific modifications in the future.

## What's in this repo?

This repo contains the following files and directories:
   * **install-wp.pp**
   * **/do-wordpress/metadata.json**
   * **/do-wordpress/manifests**
      * **conf.pp**
      * **db.pp**
      * **init.pp**
      * **web.pp**
      * **wp.pp**
   * **/do-wordpress/templates**
      * **wp-config.php.erb**

**install.pp** is a single class used to apply the changes set by the new class on the system (in this case, the class 'wordpress').

**metadata.json** contains metadata and dependency information about the module.

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

**wp-config.php.erb** is a template which generates information about the MySQL database the generated Wordpress app will use.

## Setting up Wordpress using Puppet

Install Puppet using `$sudo apt-get install puppet`, then install PuppetLabs Apache and MySQL modules with `$sudo puppet module install puppetlabs-apache` and `$sudo puppet module install puppetlabs-mysql`. Use the command `$sudo puppet module list` to check these have been installed successfully.

Navigate to the `p2p-update/puppet-items` directory. Build the module with `$sudo puppet module build do-wordpress` - this will create a tar.gz file in `puppet-items/do-wordpress/pkg` which can be shared and used for installation.

Install the new module using `$sudo puppet install ~/p2p-update/puppet-items/do-wordpress/pkg/do-wordpress-0.1.0.tar.gz`.

(To uninstall the module, simply use `$sudo puppet module uninstall do-wordpress`)

Apply the manifests given by using `$sudo puppet apply install-wp.pp`. The installation process will take a few minutes and will end with the line `Finished catalog run in x seconds` if successful.

Visit `http://[IP]` - this should display a default Wordpress page.

## Generating a new module

To generate a new module, use the command `$puppet module generate [name-of-module] --skip-interview`. This will create a directory named `[name-of-module]` containing the file `metadata.json`.

Omit the `--skip-interview` flag to enter a series of interactive command prompts which will be used to populate this file.

**_NOTE_**:`"name": "puppetlabs-stdlib"` may need to be replaced with `"name": "puppetlabs/stdlib"` in **metadata.json** due to a bug in Puppet (ver 3.7.2).
